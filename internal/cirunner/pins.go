package cirunner

// What the install downloads, each pinned by version and SHA-256 here, in
// reviewed source, and refused when what arrives does not match.
const (
	// ImageURL is Canonical's Ubuntu 24.04 cloud image, from its dated
	// release folder, which never changes, rather than release/, which
	// moves.
	ImageURL    = "https://cloud-images.ubuntu.com/releases/noble/release-20260926/ubuntu-24.04-server-cloudimg-amd64.img"
	ImageSHA256 = "6a81c37564db9b1ee84e141922625e1d7c5b389b99bb3c572e0243607d5bb4d2"
	// RunnerVersion is GitHub's Actions runner the VM runs each job with.
	// GitHub stops giving jobs to a runner some weeks after a newer release,
	// so this pin is renewed with each release, and the VM's image with it.
	RunnerVersion = "2.338.0"
	RunnerURL     = "https://github.com/actions/runner/releases/download/v2.338.0/actions-runner-linux-x64-2.338.0.tar.gz"
	RunnerSHA256  = "af4b794c1bc41d73d40535e3fe092a39f9679cd8d965954c2aca25a05ca41d32"
)
