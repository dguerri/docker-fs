package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/plesk/docker-fs/lib/log"

	"github.com/plesk/docker-fs/lib/dockerfs"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

	daemon "github.com/sevlyar/go-daemon"
)

type Manager struct {
	statusPath string
}

func New() *Manager {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("[warning] Cannot detect user home directory. Use /tmp.")
		home = "/tmp"
	}
	return &Manager{
		statusPath: filepath.Join(home, ".dockerfs.status.json"),
	}
}

func (m *Manager) ListContainers() (container_list []types.Container, err error) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	container_list, err = cli.ContainerList(ctx, types.ContainerListOptions{})
	return
}

func (m *Manager) MountContainer(containerId, mountPoint string, daemonize bool) error {
	if err := m.writeStatus(containerId, mountPoint); err != nil {
		return err
	}

	if daemonize {
		ctx := daemon.Context{}
		child, err := ctx.Reborn()
		if err != nil {
			return fmt.Errorf("daemonization failed: %w", err)
		}
		if child != nil {
			// parent process
			return nil
		}
	}

	log.Printf("[info] Check if mount directory exists (%v)...", mountPoint)
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return err
	}
	log.Printf("[info] Fetching content of container %v...", containerId)
	dockerMng := dockerfs.NewMng(containerId)
	if err := dockerMng.Init(); err != nil {
		return fmt.Errorf("dockerMng.Init() failed: %w", err)
	}

	root := dockerMng.Root()

	log.Printf("[info] Mounting FS to %v...", mountPoint)
	server, err := fs.Mount(mountPoint, root, &fs.Options{})
	if err != nil {
		return fmt.Errorf("mount failed: %w", err)
	}

	log.Printf("[info] Setting up signal handler...")
	osSignalChannel := make(chan os.Signal, 1)
	signal.Notify(osSignalChannel, syscall.SIGTERM, syscall.SIGINT)
	go shutdown(server, osSignalChannel)

	log.Printf("[info] OK!")
	server.Wait()
	log.Printf("[info] Server finished.")

	return m.writeStatus(containerId, "")
}

func (m *Manager) UnmountContainer(id, path string) error {
	cmd := exec.Command("umount", path)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
	return m.writeStatus(id, "")
}

func (m *Manager) writeStatus(id, path string) error {
	fmt.Printf("write status: %q = %q\n", id, path)
	status, err := m.ReadStatus()
	if err != nil {
		return err
	}
	if path != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		status[id] = absPath
	} else {
		delete(status, id)
	}
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	fmt.Printf("status => %s\n", data)
	return ioutil.WriteFile(m.statusPath, data, 0644)
}

func (m *Manager) ReadStatus() (map[string]string, error) {
	data, err := ioutil.ReadFile(m.statusPath)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	if err := json.Unmarshal(data, &status); err != nil {
		return nil, err
	}
	return status, nil
}

func shutdown(server *fuse.Server, signals <-chan os.Signal) {
	<-signals
	if err := server.Unmount(); err != nil {
		log.Printf("[warning] server unmount failed: %v", err)
		os.Exit(1)
	}

	log.Printf("[info] Unmount successful.")
	os.Exit(0)
}
