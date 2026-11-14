// Package buildmeta records SUB6 distribution and reviewed upstream provenance.
package buildmeta

const (
	Distribution          = "SUB6"
	UpstreamVersion       = "2.21.2"
	UpstreamCommit        = "7827a2f5f900ce50aafa37ab71ca1918dfe7cf8a"
	UpstreamReleaseCommit = "dbed7697aa56e76c6eb48c4914fa441496b67bdd"
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
