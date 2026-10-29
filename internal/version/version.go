// Package version, derleme zamanında ldflags ile doldurulan sürüm bilgisini taşır.
package version

var (
	// Version, -ldflags "-X ...=v0.1.0" ile atanır; varsayılan geliştirme değeri.
	Version = "dev"
	// Commit, kısa git hash'i.
	Commit = "unknown"
)

func String() string { return Version + " (" + Commit + ")" }
