package daemon

import (
	"bufio"
	"encoding/json"
	"runtime/debug"
	"time"

	"golang.org/x/mod/semver"

	"github.com/ryusenkei-inc/hostslot/internal/platform"
)

// Version is the module version of this binary, or "" for a development
// build without one. `go install` of a commit gives a pseudo-version, which
// sorts by commit time.
func Version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || !semver.IsValid(info.Main.Version) {
		return ""
	}
	return info.Main.Version
}

// Older reports whether a daemon of version daemonVersion should make way for
// a client of version clientVersion. Unknown versions never do, so a
// development build or a daemon from before versions were reported is left
// alone, and an outdated client never stops a newer daemon.
func Older(daemonVersion, clientVersion string) bool {
	if !semver.IsValid(daemonVersion) || !semver.IsValid(clientVersion) {
		return false
	}
	return semver.Compare(daemonVersion, clientVersion) < 0
}

// RetireOlder asks a running daemon older than version to stop accepting
// clients. It frees the IPC endpoint and the daemon lock at once, so the next
// Connect starts a daemon of this version, and it exits after its current
// jobs. It reports whether a daemon was retired.
func RetireOlder(p *platform.Platform, version string) bool {
	s, err := QueryStatus(p)
	if err != nil || !Older(s.Version, version) {
		return false
	}
	c, err := p.IPC.Dial(500 * time.Millisecond)
	if err != nil {
		return false
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(c).Encode(Message{Op: OpRetire}); err != nil {
		return false
	}
	var m Message
	return readMessage(bufio.NewReader(c), &m) == nil && m.Type == TypeRetired
}
