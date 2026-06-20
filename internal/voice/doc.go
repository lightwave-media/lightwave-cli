// Package voice implements v_speak registry resolution, ceremony sessions,
// and nullhub nullvoice synthesis for lw voice commands.
package voice

import "time"

const (
	dirPerm            = 0o755
	filePerm           = 0o644
	httpStatusErrorMin = 300
	synthesizeTimeout  = 2 * time.Minute
)
