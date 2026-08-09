package version

import "runtime"

var (
	Version   = "0.1.0-dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
	Edition   = "Community"
)

type Info struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	GitCommit string `json:"gitCommit"`
	BuildDate string `json:"buildDate"`
	Edition   string `json:"edition"`
	GoVersion string `json:"goVersion"`
}

func Current() Info {
	return Info{Name: "Relio", Version: Version, GitCommit: GitCommit, BuildDate: BuildDate, Edition: Edition, GoVersion: runtime.Version()}
}
