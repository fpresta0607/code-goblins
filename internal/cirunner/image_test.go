package cirunner

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// firstBoot is what the test reads of the VM's first boot settings.
type firstBoot struct {
	Users []struct {
		Name       string   `yaml:"name"`
		LockPasswd bool     `yaml:"lock_passwd"`
		Sudo       any      `yaml:"sudo"`
		Keys       []string `yaml:"ssh_authorized_keys"`
		Groups     any      `yaml:"groups"`
	} `yaml:"users"`
	Packages   []string `yaml:"packages"`
	WriteFiles []struct {
		Path        string `yaml:"path"`
		Permissions string `yaml:"permissions"`
		Content     string `yaml:"content"`
	} `yaml:"write_files"`
	RunCmd     [][]string `yaml:"runcmd"`
	PowerState struct {
		Mode      string `yaml:"mode"`
		Condition string `yaml:"condition"`
	} `yaml:"power_state"`
}

func (f firstBoot) file(t *testing.T, path string) string {
	t.Helper()
	for _, file := range f.WriteFiles {
		if file.Path == path {
			return file.Content
		}
	}
	t.Fatalf("the first boot writes no %s", path)
	return ""
}

func readFirstBoot(t *testing.T) (string, firstBoot) {
	t.Helper()
	rendered, err := UserData()
	if err != nil {
		t.Fatal(err)
	}
	var boot firstBoot
	if err := yaml.Unmarshal([]byte(rendered), &boot); err != nil {
		t.Fatalf("the first boot's settings are not YAML: %v", err)
	}
	return rendered, boot
}

// The first boot installs Docker and the runner pinned in pins.go, checked
// against its SHA-256 before it is unpacked, and makes runner a user no one
// can sign in as, with no sudo.
func TestUserDataPinsTheRunnerAndInstallsDocker(t *testing.T) {
	// Act
	rendered, boot := readFirstBoot(t)

	// Assert
	if strings.Contains(rendered, "\r") {
		t.Fatal("the settings hold a carriage return, which breaks the VM's shell scripts; want image/ checked out with LF endings, as .gitattributes says")
	}
	if !strings.HasPrefix(rendered, "#cloud-config\n") || strings.Contains(rendered, "{{") {
		t.Fatalf("the settings start %q and hold a template mark %v; want cloud-config, rendered whole", rendered[:min(len(rendered), 20)], strings.Contains(rendered, "{{"))
	}
	if len(boot.Users) != 1 || boot.Users[0].Name != "runner" || !boot.Users[0].LockPasswd || boot.Users[0].Sudo != nil || len(boot.Users[0].Keys) != 0 {
		t.Errorf("users %+v; want runner alone, with no password, no key and no sudo, and no default user", boot.Users)
	}
	if !slices.Contains(boot.Packages, "docker.io") {
		t.Errorf("packages %q; want docker.io", boot.Packages)
	}
	setup := boot.file(t, "/usr/local/sbin/cg-ci-setup.sh")
	fetch := strings.Index(setup, "curl -fsSL -o /tmp/actions-runner.tar.gz '"+RunnerURL+"'")
	check := strings.Index(setup, "echo '"+RunnerSHA256+"  /tmp/actions-runner.tar.gz' | sha256sum -c -")
	unpack := strings.Index(setup, "tar -xzf /tmp/actions-runner.tar.gz")
	if !strings.HasPrefix(setup, "#!/bin/sh\nset -eu\n") || fetch < 0 || check < fetch || unpack < check {
		t.Errorf("setup:\n%s\nwant it to stop at its first failure, and to fetch the pinned runner, check its SHA-256, then unpack it", setup)
	}
	if !strings.Contains(setup, "usermod -aG docker runner") || !strings.Contains(setup, "echo PYTEST_XDIST_AUTO_NUM_WORKERS=2 > /opt/actions-runner/.env") {
		t.Errorf("setup:\n%s\nwant runner in the docker group and the shards' two pytest workers", setup)
	}
}

// Only a setup that ran to its end powers the VM off for its checkpoint, and
// cloud-init never runs again from it, at a job's boot.
func TestUserDataPowersOffOnlyAMadeVM(t *testing.T) {
	// Act
	_, boot := readFirstBoot(t)

	// Assert
	setup := boot.file(t, "/usr/local/sbin/cg-ci-setup.sh")
	lines := strings.Split(strings.TrimSpace(setup), "\n")
	if lines[len(lines)-1] != "touch /var/lib/cg-ci-made" || lines[len(lines)-2] != "touch /etc/cloud/cloud-init.disabled" {
		t.Errorf("setup ends %q; want cloud-init turned off, then the VM marked made, last", lines[len(lines)-2:])
	}
	if boot.PowerState.Mode != "poweroff" || boot.PowerState.Condition != "test -f /var/lib/cg-ci-made" {
		t.Errorf("power state %+v; want a power-off on the made mark alone", boot.PowerState)
	}
	if len(boot.RunCmd) != 1 || !slices.Equal(boot.RunCmd[0], []string{"/usr/local/sbin/cg-ci-setup.sh"}) {
		t.Errorf("runcmd %q; want the setup script alone, so nothing runs past a failure", boot.RunCmd)
	}
}

// A job runs only at a boot with its DVD, and the VM powers off once the job
// ends, however it ends.
func TestUserDataRunsAJobOnlyWithItsDVD(t *testing.T) {
	// Act
	_, boot := readFirstBoot(t)

	// Assert
	unit := boot.file(t, "/etc/systemd/system/cg-ci-job.service")
	for _, line := range []string{`Requires=dev-disk-by\x2dlabel-` + DVDLabel + ".device", "ExecStopPost=/usr/bin/systemctl poweroff", "ExecStart=/usr/local/bin/cg-ci-job.sh"} {
		if !strings.Contains(unit, line+"\n") {
			t.Errorf("unit:\n%s\nwant %q", unit, line)
		}
	}
	job := boot.file(t, "/usr/local/bin/cg-ci-job.sh")
	if !strings.Contains(job, "mount -o ro /dev/disk/by-label/"+DVDLabel+" /mnt/cgjob") || !strings.Contains(job, "/mnt/cgjob/jitconfig") || !strings.Contains(job, "exec runuser -u runner -- /opt/actions-runner/run.sh --jitconfig") {
		t.Errorf("job:\n%s\nwant the DVD's jitconfig read and the runner run as runner", job)
	}
}
