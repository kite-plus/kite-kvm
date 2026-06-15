package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"

	"github.com/kite-plus/kite-kvm/internal/model"
)

// SQLiteStore is a Store backed by an embedded SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, applies pending
// migrations, and returns a ready Store.
func Open(ctx context.Context, path string) (*SQLiteStore, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create state dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite is a single-writer engine; serialize access to avoid SQLITE_BUSY.
	db.SetMaxOpenConns(1)

	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Close closes the database.
func (s *SQLiteStore) Close() error { return s.db.Close() }

// --- time/json helpers -----------------------------------------------------

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}

func parseTimePtr(ns sql.NullString) *time.Time {
	if !ns.Valid || ns.String == "" {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}

func marshalStrings(ss []string) string {
	if len(ss) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ss)
	return string(b)
}

func unmarshalStrings(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// --- VMs -------------------------------------------------------------------

func (s *SQLiteStore) CreateVM(ctx context.Context, vm *model.VM) error {
	now := time.Now().UTC()
	if vm.CreatedAt.IsZero() {
		vm.CreatedAt = now
	}
	vm.UpdatedAt = now
	_, err := s.db.ExecContext(ctx, `INSERT INTO vms (
        id, domain_name, domain_uuid, hostname, flavor_id, image_id,
        vcpus, memory_mb, disk_gb, network_id, network_mode, mac, ip,
        gateway, netmask, status, power_state, prev_power_state,
        disk_path, seed_path, password, ssh_keys, created_at, updated_at
    ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		vm.ID, vm.DomainName, vm.DomainUUID, vm.Hostname, vm.FlavorID, vm.ImageID,
		vm.VCPUs, vm.MemoryMB, vm.DiskGB, vm.NetworkID, string(vm.NetworkMode), vm.MAC, vm.IP,
		vm.Gateway, vm.Netmask, string(vm.Status), string(vm.PowerState), string(vm.PrevPowerState),
		vm.DiskPath, vm.SeedPath, vm.Password, marshalStrings(vm.SSHKeys), fmtTime(vm.CreatedAt), fmtTime(vm.UpdatedAt),
	)
	if err != nil {
		return mapConstraintErr(err)
	}
	return nil
}

const vmColumns = `id, domain_name, domain_uuid, hostname, flavor_id, image_id,
    vcpus, memory_mb, disk_gb, network_id, network_mode, mac, ip,
    gateway, netmask, status, power_state, prev_power_state,
    disk_path, seed_path, password, ssh_keys, created_at, updated_at`

func scanVM(sc interface{ Scan(...any) error }) (*model.VM, error) {
	var (
		vm                            model.VM
		mode, status, power, prevPow  string
		sshKeys, createdAt, updatedAt string
	)
	if err := sc.Scan(
		&vm.ID, &vm.DomainName, &vm.DomainUUID, &vm.Hostname, &vm.FlavorID, &vm.ImageID,
		&vm.VCPUs, &vm.MemoryMB, &vm.DiskGB, &vm.NetworkID, &mode, &vm.MAC, &vm.IP,
		&vm.Gateway, &vm.Netmask, &status, &power, &prevPow,
		&vm.DiskPath, &vm.SeedPath, &vm.Password, &sshKeys, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	vm.NetworkMode = model.NetworkMode(mode)
	vm.Status = model.VMStatus(status)
	vm.PowerState = model.PowerState(power)
	vm.PrevPowerState = model.PowerState(prevPow)
	vm.SSHKeys = unmarshalStrings(sshKeys)
	vm.CreatedAt = parseTime(createdAt)
	vm.UpdatedAt = parseTime(updatedAt)
	return &vm, nil
}

func (s *SQLiteStore) GetVM(ctx context.Context, id string) (*model.VM, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+vmColumns+` FROM vms WHERE id = ?`, id)
	vm, err := scanVM(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return vm, err
}

func (s *SQLiteStore) ListVMs(ctx context.Context) ([]*model.VM, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+vmColumns+` FROM vms ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var vms []*model.VM
	for rows.Next() {
		vm, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		vms = append(vms, vm)
	}
	return vms, rows.Err()
}

func (s *SQLiteStore) UpdateVM(ctx context.Context, vm *model.VM) error {
	vm.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `UPDATE vms SET
        domain_name=?, domain_uuid=?, hostname=?, flavor_id=?, image_id=?,
        vcpus=?, memory_mb=?, disk_gb=?, network_id=?, network_mode=?, mac=?, ip=?,
        gateway=?, netmask=?, status=?, power_state=?, prev_power_state=?,
        disk_path=?, seed_path=?, password=?, ssh_keys=?, updated_at=?
        WHERE id=?`,
		vm.DomainName, vm.DomainUUID, vm.Hostname, vm.FlavorID, vm.ImageID,
		vm.VCPUs, vm.MemoryMB, vm.DiskGB, vm.NetworkID, string(vm.NetworkMode), vm.MAC, vm.IP,
		vm.Gateway, vm.Netmask, string(vm.Status), string(vm.PowerState), string(vm.PrevPowerState),
		vm.DiskPath, vm.SeedPath, vm.Password, marshalStrings(vm.SSHKeys), fmtTime(vm.UpdatedAt),
		vm.ID,
	)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *SQLiteStore) DeleteVM(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM vms WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

// --- Jobs ------------------------------------------------------------------

func (s *SQLiteStore) CreateJob(ctx context.Context, job *model.Job) error {
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs (
        id, type, vm_id, state, error, idempotency_key, created_at, started_at, finished_at
    ) VALUES (?,?,?,?,?,?,?,?,?)`,
		job.ID, string(job.Type), job.VMID, string(job.State), job.Error, job.IdempotencyKey,
		fmtTime(job.CreatedAt), fmtTimePtr(job.StartedAt), fmtTimePtr(job.FinishedAt),
	)
	if err != nil {
		return mapConstraintErr(err)
	}
	return nil
}

const jobColumns = `id, type, vm_id, state, error, idempotency_key, created_at, started_at, finished_at`

func scanJob(sc interface{ Scan(...any) error }) (*model.Job, error) {
	var (
		job                  model.Job
		typ, state           string
		createdAt            string
		startedAt, finishedAt sql.NullString
	)
	if err := sc.Scan(
		&job.ID, &typ, &job.VMID, &state, &job.Error, &job.IdempotencyKey,
		&createdAt, &startedAt, &finishedAt,
	); err != nil {
		return nil, err
	}
	job.Type = model.JobType(typ)
	job.State = model.JobState(state)
	job.CreatedAt = parseTime(createdAt)
	job.StartedAt = parseTimePtr(startedAt)
	job.FinishedAt = parseTimePtr(finishedAt)
	return &job, nil
}

func (s *SQLiteStore) GetJob(ctx context.Context, id string) (*model.Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = ?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return job, err
}

func (s *SQLiteStore) UpdateJob(ctx context.Context, job *model.Job) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET
        type=?, vm_id=?, state=?, error=?, idempotency_key=?, started_at=?, finished_at=?
        WHERE id=?`,
		string(job.Type), job.VMID, string(job.State), job.Error, job.IdempotencyKey,
		fmtTimePtr(job.StartedAt), fmtTimePtr(job.FinishedAt), job.ID,
	)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

func (s *SQLiteStore) ListJobsByState(ctx context.Context, state model.JobState) ([]*model.Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE state = ? ORDER BY created_at`, string(state))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*model.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// --- Idempotency -----------------------------------------------------------

func (s *SQLiteStore) GetIdempotency(ctx context.Context, key string) (*model.IdempotencyRecord, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT key, job_id, request_hash, response, status_code, created_at, expires_at
         FROM idempotency_keys WHERE key = ?`, key)
	var (
		rec                  model.IdempotencyRecord
		response             []byte
		createdAt, expiresAt string
	)
	if err := row.Scan(&rec.Key, &rec.JobID, &rec.RequestHash, &response, &rec.StatusCode, &createdAt, &expiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	rec.Response = response
	rec.CreatedAt = parseTime(createdAt)
	rec.ExpiresAt = parseTime(expiresAt)
	return &rec, nil
}

func (s *SQLiteStore) PutIdempotency(ctx context.Context, rec *model.IdempotencyRecord) error {
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO idempotency_keys (key, job_id, request_hash, response, status_code, created_at, expires_at)
         VALUES (?,?,?,?,?,?,?)`,
		rec.Key, rec.JobID, rec.RequestHash, rec.Response, rec.StatusCode, fmtTime(rec.CreatedAt), fmtTime(rec.ExpiresAt))
	if err != nil {
		return mapConstraintErr(err)
	}
	return nil
}

func (s *SQLiteStore) UpdateIdempotency(ctx context.Context, rec *model.IdempotencyRecord) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE idempotency_keys SET job_id=?, response=?, status_code=? WHERE key=?`,
		rec.JobID, rec.Response, rec.StatusCode, rec.Key)
	if err != nil {
		return err
	}
	return requireAffected(res)
}

// --- IP allocations --------------------------------------------------------

func (s *SQLiteStore) AllocateIP(ctx context.Context, networkID, vmID, mac string, candidates []string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	taken := map[string]struct{}{}
	rows, err := tx.QueryContext(ctx, `SELECT ip FROM ip_allocations WHERE network_id = ?`, networkID)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			_ = rows.Close()
			return "", err
		}
		taken[ip] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return "", err
	}
	_ = rows.Close()

	for _, ip := range candidates {
		if _, ok := taken[ip]; ok {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ip_allocations (network_id, ip, vm_id, mac, created_at) VALUES (?,?,?,?,?)`,
			networkID, ip, vmID, mac, fmtTime(time.Now().UTC())); err != nil {
			return "", err
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return ip, nil
	}
	return "", ErrNoIPAvailable
}

func (s *SQLiteStore) ReleaseIPByVM(ctx context.Context, vmID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM ip_allocations WHERE vm_id = ?`, vmID)
	return err
}

func (s *SQLiteStore) AllocatedIPs(ctx context.Context, networkID string) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT ip FROM ip_allocations WHERE network_id = ?`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var ip string
		if err := rows.Scan(&ip); err != nil {
			return nil, err
		}
		out[ip] = struct{}{}
	}
	return out, rows.Err()
}

// --- helpers ---------------------------------------------------------------

func requireAffected(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func mapConstraintErr(err error) error {
	if err == nil {
		return nil
	}
	// modernc.org/sqlite reports constraint violations with this substring.
	if strings.Contains(err.Error(), "constraint failed") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}
