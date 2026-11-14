package hub

import (
	"errors"
	"net/http"
	"time"
)

// Bound a stalled write rather than the total duration of a legitimate large download.
type idleDownloadWriter struct {
	http.ResponseWriter
	idle time.Duration
}

func (w idleDownloadWriter) Write(p []byte) (int, error) {
	if e := http.NewResponseController(w.ResponseWriter).SetWriteDeadline(time.Now().Add(w.idle)); e != nil && !errors.Is(e, http.ErrNotSupported) {
		return 0, e
	}
	return w.ResponseWriter.Write(p)
}
