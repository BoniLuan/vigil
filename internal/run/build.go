package run

type BuildInfo struct {
	Version string
	Commit  string
}

func checkerUserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return "Vigil/" + version
}
