package main

import (
	"context"
	"dagger/stereosd/internal/dagger"
)

type Stereosd struct {
	Source *dagger.Directory
}

// New creates a new StereOS module instance.
func New(
	// Directory of the flake's source
	//
	// +defaultPath="/"
	// +ignore=["build", ".git"]
	Source *dagger.Directory,
) *Stereosd {
	return &Stereosd{
		Source,
	}
}

func (m *Stereosd) nixOSContainer(src *dagger.Directory) *dagger.Container {
	// Extract the base image's /nix directory (store + var/nix DB) BEFORE
	// any source-dependent layers. This Directory reference is stable
	// (depends only on the base image), so it never changes between runs.
	baseNix := dag.Container().
		From("nixos/nix:latest").
		Directory("/nix")

	return dag.Container().
		From("nixos/nix:latest").

		// Enable flakes via env var rather than editing /etc/nix/nix.conf,
		// because nix.conf is a symlink into /nix/store and the cache
		// mount below replaces /nix contents.
		WithEnvVariable("NIX_CONFIG", "experimental-features = nix-command flakes").

		// Persistent cache over the entire /nix tree (store paths + Nix
		// DB). Seeded once from the base image. The DB and store stay in
		// sync so Nix recognises previously downloaded paths on every run.
		WithMountedCache("/nix", dag.CacheVolume("nix"), dagger.ContainerWithMountedCacheOpts{
			Source: baseNix,
		}).

		// Copy source flake into the container.
		WithDirectory("/workspace", src).
		WithWorkdir("/workspace").

		// Create a self-contained git repo so Nix sees a valid flake root.
		// The host .git may be a worktree pointer whose target doesn't
		// exist inside the container; a fresh init side-steps that.
		WithExec([]string{
			"sh", "-c",
			"git init && " +
				"git config user.email 'ci@stereos.ai' && " +
				"git config user.name 'CI' && " +
				"git add -A && " +
				"git commit -m init-flake",
		}).

		// Pre-download the dev shell so subsequent commands are fast.
		WithExec([]string{"nix", "develop", "--command", "true"})
}

// wrapNixDev wraps a command to run inside the Nix dev shell.
// Use this for all WithExec calls that need dev tools (go, make, hurl, etc.).
func wrapNixDev(args ...string) []string {
	return append([]string{"nix", "develop", "--command"}, args...)
}

// Test runs the Go test suite.
//
// +check
func (m *Stereosd) Test(ctx context.Context) (string, error) {
	return m.nixOSContainer(m.Source).
		WithExec(wrapNixDev("ginkgo", "run", "--succinct", "-p", "./...")).
		Stdout(ctx)
}
