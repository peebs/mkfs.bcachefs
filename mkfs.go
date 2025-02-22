package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gokrazy/gokapi"
	"github.com/gokrazy/gokapi/ondeviceapi"
	"github.com/gokrazy/internal/rootdev"
)

func makeFilesystemNotWar() error {
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 5 {
			continue
		}
		mountpoint := parts[4]
		log.Printf("Found mountpoint %q", parts[4])
		if mountpoint == "/perm" {
			log.Printf("/perm file system already mounted, nothing to do")
			return nil
		}
	}

	// /perm is not a mounted file system. Try to create a file system.
	dev := rootdev.Partition(rootdev.Perm)
	log.Printf("No /perm mountpoint found. Creating file system on %s", dev)
	tmp, err := os.MkdirTemp("", "gokrazy-bcachefs-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	log.Printf("Writing self-contained bcachefs-tools to %s", tmp)

	// write each file from embedded dir
	dirents, err := bcachefsEmbedded.ReadDir(bcachefsRoot)
	for _, d := range dirents {
		b, err := bcachefsEmbedded.ReadFile(filepath.Join(bcachefsRoot, d.Name()))
		if err != nil {
			return err
		}
		fn := filepath.Join(tmp, d.Name())
		if err := os.WriteFile(fn, b, 755); err != nil {
			return err
		}
	}

	// exec bcachefs format on rootdev partition
	mkfs := exec.Command(filepath.Join(tmp, "ld-linux-aarch64.so.1"), filepath.Join(tmp, "bcachefs"), "format", "--block_size=4096", dev)
	mkfs.Env = append(os.Environ(), "LD_LIBRARY_PATH="+tmp)
	mkfs.Stdout = os.Stdout
	mkfs.Stderr = os.Stderr
	log.Printf("exec bcachefs with args: %v", mkfs.Args)
	if err := mkfs.Run(); err != nil {
		return fmt.Errorf("%v: %v", mkfs.Args, err)
	}
	log.Printf("Success formatting rootdev perm partition!")

	// It is pointless to try and mount the file system here from within this
	// process, as gokrazy services are run in a separate mount namespace.
	// Instead, we trigger a reboot so that /perm is mounted early and
	// the whole system picks it up correctly.
	log.Printf("triggering reboot to mount /perm")
	cfg, err := gokapi.ConnectOnDevice()
	if err != nil {
		return err
	}
	cl := ondeviceapi.NewAPIClient(cfg)
	_, err = cl.UpdateApi.Reboot(context.Background(), &ondeviceapi.UpdateApiRebootOpts{})
	if err != nil {
		return err
	}

	return nil
}

// readConfigFile reads configuration files from /perm /etc or / and returns
// trimmed content as string.
//
// TODO: de-duplicate this with gokrazy.go into a gokrazy/internal package
func readConfigFile(fileName string) (string, error) {
	str, err := os.ReadFile("/perm/" + fileName)
	if err != nil {
		str, err = os.ReadFile("/etc/" + fileName)
	}
	if err != nil && os.IsNotExist(err) {
		str, err = os.ReadFile("/" + fileName)
	}

	return strings.TrimSpace(string(str)), err
}

func main() {
	if err := makeFilesystemNotWar(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
	}
	// tell gokrazy to not supervise this service, it’s a one-off:
	os.Exit(125)
}
