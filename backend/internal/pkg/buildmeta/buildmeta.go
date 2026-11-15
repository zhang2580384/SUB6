// Package buildmeta records SUB6 distribution and reviewed upstream provenance.
package buildmeta

const (
	Distribution          = "SUB6"
	UpstreamVersion       = "2.21.3"
	UpstreamCommit        = "aa8e596e6d6f3e4648d6beba73b39b240dd5357a"
	UpstreamReleaseCommit = "aa8e596e6d6f3e4648d6beba73b39b240dd5357a"
)

// Revision and BuiltAt are initialized by the executable before serving requests.
var Revision = "unknown"
var BuiltAt = "unknown"

type Info struct {
	Distribution          string `json:"distribution"`
	UpstreamVersion       string `json:"upstream_version"`
	UpstreamCommit        string `json:"upstream_commit"`
	UpstreamReleaseCommit string `json:"upstream_release_commit"`
	BuildCommit           string `json:"build_commit"`
	BuiltAt               string `json:"built_at"`
}

func Current() Info {
	return Info{Distribution, UpstreamVersion, UpstreamCommit, UpstreamReleaseCommit, Revision, BuiltAt}
}
