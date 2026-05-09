package version

import "runtime/debug"

var (
	// Version is set by release builds.
	Version = "dev"
)

type BuildInfo struct {
	Service   string   `json:"service"`
	Version   string   `json:"version"`
	GoVersion string   `json:"go_version"`
	Module    string   `json:"module"`
	VCS       *VCSInfo `json:"vcs,omitempty"`
}

type VCSInfo struct {
	Revision string `json:"revision,omitempty"`
	Time     string `json:"time,omitempty"`
	Modified bool   `json:"modified"`
}

func CurrentBuildInfo(service string) BuildInfo {
	info := BuildInfo{
		Service: service,
		Version: Version,
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	info.GoVersion = build.GoVersion
	info.Module = build.Path

	vcs := &VCSInfo{}
	hasVCS := false
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			vcs.Revision = setting.Value
			hasVCS = true
		case "vcs.time":
			vcs.Time = setting.Value
			hasVCS = true
		case "vcs.modified":
			vcs.Modified = setting.Value == "true"
			hasVCS = true
		}
	}
	if hasVCS {
		info.VCS = vcs
	}
	return info
}
