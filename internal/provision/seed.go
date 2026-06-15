package provision

import (
	"fmt"
	"os"

	diskfs "github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

// SeedFile is one file placed at the root of the seed ISO.
type SeedFile struct {
	Name    string
	Content []byte
}

// seedISOSize is the backing image size for the cidata seed. The cloud-init
// payload is a few KB; this leaves ample headroom.
const seedISOSize = 4 * 1024 * 1024

// BuildSeedISO writes a cloud-init NoCloud seed ISO at path. The volume is
// labeled "cidata" with Rock Ridge extensions so cloud-init's NoCloud
// datasource discovers it and reads the long filenames. Pure Go — no external
// tooling required.
func BuildSeedISO(path string, files []SeedFile) error {
	// go-diskfs creates the backing file; remove any stale image first.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale seed: %w", err)
	}

	// ISO9660 requires a 2048-byte logical block size.
	d, err := diskfs.Create(path, seedISOSize, diskfs.SectorSize(2048))
	if err != nil {
		return fmt.Errorf("create seed image: %w", err)
	}

	workdir, err := os.MkdirTemp("", "kite-seed-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(workdir) }()

	fs, err := d.CreateFilesystem(disk.FilesystemSpec{
		Partition:   0,
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "cidata",
		WorkDir:     workdir,
	})
	if err != nil {
		return fmt.Errorf("create iso filesystem: %w", err)
	}

	for _, f := range files {
		w, err := fs.OpenFile("/"+f.Name, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("create %s: %w", f.Name, err)
		}
		if _, err := w.Write(f.Content); err != nil {
			return fmt.Errorf("write %s: %w", f.Name, err)
		}
	}

	iso, ok := fs.(*iso9660.FileSystem)
	if !ok {
		return fmt.Errorf("unexpected filesystem type %T", fs)
	}
	if err := iso.Finalize(iso9660.FinalizeOptions{
		RockRidge:        true,
		VolumeIdentifier: "cidata",
	}); err != nil {
		return fmt.Errorf("finalize seed iso: %w", err)
	}
	return nil
}
