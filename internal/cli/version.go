package cli

import (
	"runtime"
	"runtime/debug"

	"github.com/allenvissong-source/starry-cli/pkg/transport"
)

var Version = "development"
var ReleaseID = "development"
var SourceCommit = ""
var SourceDigest = ""
var BuildTime = ""
var SourceDirty = "unknown"

func versionInfo() map[string]any {
	commit, dirty := SourceCommit, SourceDirty
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if commit == "" && setting.Key == "vcs.revision" {
				commit = setting.Value
			}
			if dirty == "unknown" && setting.Key == "vcs.modified" {
				dirty = setting.Value
			}
		}
	}
	return map[string]any{"version": Version, "releaseId": ReleaseID, "sourceCommit": commit,
		"sourceDigest": SourceDigest, "sourceDirty": dirty, "builtAt": BuildTime,
		"os": runtime.GOOS, "arch": runtime.GOARCH, "goVersion": runtime.Version(),
		"defaultEndpoints": map[string]string{"backendUrl": transport.ProductionBackendURL,
			"publicWebUrl": transport.ProductionPublicWebURL, "casDirectUploadOrigin": transport.ProductionCASDirectUploadOrigin}}
}
