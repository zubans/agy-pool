// Package version хранит версию сборки сервиса.
package version

// Version задаётся при сборке: -ldflags "-X github.com/zubans/agy-pool/internal/version.Version=…".
var Version = "dev"
