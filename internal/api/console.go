package api

import "net/http"

// reserved returns a handler for an endpoint whose shape is defined but whose
// implementation is deferred. It responds 501 Not Implemented so clients can
// detect the capability without the route 404-ing.
func reserved(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeError(w, errNotImplemented(feature+" is not implemented in this version"))
	}
}
