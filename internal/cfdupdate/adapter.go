package cfdupdate

import (
	"context"

	"github.com/nue-mic/cloudflared-manager/internal/cfdbin"
)

// StoreAdapter binds a concrete *cfdbin.Store and *cfdbin.Downloader into the
// BinaryStore interface, hiding the fact that cfdbin.Store.Install takes the
// downloader as an argument. This keeps the updater's interface clean and
// fakeable in tests.
type StoreAdapter struct {
	Store *cfdbin.Store
	DL    *cfdbin.Downloader
}

// NewStoreAdapter wraps the store + downloader for the updater.
func NewStoreAdapter(store *cfdbin.Store, dl *cfdbin.Downloader) *StoreAdapter {
	return &StoreAdapter{Store: store, DL: dl}
}

func (a *StoreAdapter) ActiveVersion() string { return a.Store.ActiveVersion() }

func (a *StoreAdapter) Install(ctx context.Context, version string) (cfdbin.VersionMeta, error) {
	return a.Store.Install(ctx, a.DL, version)
}

func (a *StoreAdapter) Activate(version string) error { return a.Store.Activate(version) }

func (a *StoreAdapter) List() ([]cfdbin.InstalledVersion, error) { return a.Store.List() }

func (a *StoreAdapter) Delete(version string) error { return a.Store.Delete(version) }

// Compile-time assertion that the adapter satisfies the interface.
var _ BinaryStore = (*StoreAdapter)(nil)
