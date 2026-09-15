package maintenance

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ArchiveDir is where rotated logs land, relative to the print root.
const ArchiveDir = logZone + "/archive"

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

// Rotation is one planned log rotation.
type Rotation struct {
	// Source is the live log, relative to the print root.
	Source string `json:"source"`
	// Archive is where its contents will be written, relative to the print root.
	Archive string `json:"archive"`
	Bytes   int64  `json:"bytes"`
}

// PlanRotations turns oversize logs into rotation steps.
//
// The archive name carries the scan time rather than a serial number, so two
// rotations of the same log never collide and the ordering is readable without
// opening anything.
//
// It keeps the log's path below the zone rather than just its basename:
// launchd/ holds `maintenance.fast.stdout.log` and a sibling directory may hold
// another file of the same name, and two logs rotated in the same second would
// then compete for one archive name. O_EXCL makes that an error rather than a
// clobber, but an error is still a log that did not get rotated, and the
// surviving archive would not say which of the two it came from.
func PlanRotations(oversize []OversizeFile, now time.Time) []Rotation {
	stamp := now.UTC().Format("2006-01-02T150405Z")

	rotations := make([]Rotation, 0, len(oversize))

	for _, file := range oversize {
		within := strings.TrimPrefix(file.Path, logZone+"/")

		rotations = append(rotations, Rotation{
			Source:  file.Path,
			Archive: ArchiveDir + "/" + within + "." + stamp + ".gz",
			Bytes:   file.Bytes,
		})
	}

	return rotations
}

// Rotate copies a log into a gzipped archive and then truncates it in place.
//
// Copy-truncate, not rename, because every writer here holds the log open in
// append mode. Renaming leaves those writers appending to an inode with no
// name, so the rotation silently discards everything written until each daemon
// restarts. Truncating the original keeps every open descriptor valid.
//
// The archive is written and closed in full BEFORE the truncate. A crash
// between the two loses nothing; the reverse order would lose the window.
func Rotate(root string, rotation Rotation) error {
	source := filepath.Join(root, rotation.Source)
	archive := filepath.Join(root, rotation.Archive)

	if err := os.MkdirAll(filepath.Dir(archive), dirPerm); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}

	// O_EXCL so a rotation can never overwrite an existing archive. Two agents
	// rotating the same log in the same second is rare and silent; losing one
	// of the two archives to a clobber would be worse than an error.
	out, err := os.OpenFile(archive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if err != nil {
		return fmt.Errorf("create archive %s: %w", rotation.Archive, err)
	}

	if err := writeGzip(out, source); err != nil {
		out.Close()
		// A partial archive must not look like a complete one. Nothing useful
		// can be done if the cleanup itself fails; the write error is the one
		// worth reporting.
		_ = os.Remove(archive)

		return err
	}

	if err := out.Close(); err != nil {
		return fmt.Errorf("close archive %s: %w", rotation.Archive, err)
	}

	if err := os.Truncate(source, 0); err != nil {
		return fmt.Errorf("truncate %s: %w", rotation.Source, err)
	}

	return nil
}

func writeGzip(out io.Writer, source string) error {
	in, err := os.Open(source) //nolint:gosec // a path the scan enumerated under the print root
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer in.Close()

	zw := gzip.NewWriter(out)

	if _, err := io.Copy(zw, in); err != nil {
		_ = zw.Close() // the copy error is the real one; the flush cannot improve it

		return fmt.Errorf("compress %s: %w", source, err)
	}

	if err := zw.Close(); err != nil {
		return fmt.Errorf("finish gzip for %s: %w", source, err)
	}

	return nil
}

// PlanPrune selects which empty directories may be removed.
//
// Only directories under a zone the stamp marks wipe_on_reset are eligible. An
// empty dir under an AUTHORED zone is reported by the scan and left alone: it
// is more likely a directory whose contents have not been written yet than
// garbage, and the stamp says losing anything there is unrecoverable.
//
// A dir the zone map does not classify at all is also skipped. Unclassified
// means no policy, and no policy is not permission — the scan surfaces those
// separately so the stamp gets fixed instead.
func PlanPrune(empty []string, shape Shape) (eligible, protected []string) {
	for _, dir := range empty {
		top := TopLevelOf(dir)
		if shape.Wipeable[top] {
			eligible = append(eligible, dir)
			continue
		}

		protected = append(protected, dir)
	}

	return eligible, protected
}

// Prune removes one empty directory.
//
// os.Remove, never RemoveAll: it fails on a non-empty directory, so a race
// that fills the directory between the scan and the prune loses nothing. The
// caller reports that failure rather than retrying harder.
func Prune(root, dir string) error {
	if err := os.Remove(filepath.Join(root, dir)); err != nil {
		return fmt.Errorf("prune %s: %w", dir, err)
	}

	return nil
}

// TopLevelOf returns the first path segment of a print-relative path, which is
// the top-level dir the zone map classifies.
func TopLevelOf(rel string) string {
	for i := 0; i < len(rel); i++ {
		if rel[i] == filepath.Separator {
			return rel[:i]
		}
	}

	return rel
}
