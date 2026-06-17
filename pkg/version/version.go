package version

// These values are overridden at build time via -ldflags -X (see
// .goreleaser.yml and deploy/Dockerfile). Defaults are placeholders so
// `go run` works during development.
var (
	Number = "0.0.30"
	// BuildDate is the day that this program was built
	BuildDate = "unknown"
)
