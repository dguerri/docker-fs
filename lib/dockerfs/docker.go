package dockerfs

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"path/filepath"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

type dockerMng interface {
	// returns read-closer to tar-archive fetched by /containers/{id}/export api method
	ContainerExport(ctx context.Context) (io.ReadCloser, error)

	GetPathAttrs(ctx context.Context, path string) (types.ContainerPathStat, error)

	GetFsChanges(ctx context.Context) ([]container.ContainerChangeResponseItem, error)

	// Get plain file content
	GetFile(ctx context.Context, path string) (io.ReadCloser, error)

	// Save file
	SaveFile(ctx context.Context, path string, data []byte, stat *types.ContainerPathStat) (err error)

	// List containers
	ContainersList(ctx context.Context) ([]types.Container, error)
}

var _ = (dockerMng)((*dockerMngImpl)(nil))

type dockerMngImpl struct {
	dockerClient *client.Client
	id           string
}

func NewDockerMng(cli *client.Client, containerId string) dockerMng {
	return &dockerMngImpl{
		dockerClient: cli,
		id:           containerId,
	}
}

func (d *dockerMngImpl) ContainerExport(ctx context.Context) (readr io.ReadCloser, err error) {
	readr, err = d.dockerClient.ContainerExport(ctx, d.id)
	return
}

func (d *dockerMngImpl) GetPathAttrs(ctx context.Context, path string) (path_stat types.ContainerPathStat, err error) {
	path_stat, err = d.dockerClient.ContainerStatPath(ctx, d.id, path)
	return
}

func (d *dockerMngImpl) GetFsChanges(ctx context.Context) (changes []container.ContainerChangeResponseItem, err error) {
	changes, err = d.dockerClient.ContainerDiff(ctx, d.id)
	return
}

func (d *dockerMngImpl) GetFile(ctx context.Context, path string) (readr io.ReadCloser, err error) {
	readr, _, err = d.dockerClient.CopyFromContainer(ctx, d.id, path)
	return
}

func (d *dockerMngImpl) ContainersList(ctx context.Context) (container_list []types.Container, err error) {
	container_list, err = d.dockerClient.ContainerList(ctx, types.ContainerListOptions{})
	return
}

// Save file content.
func (d *dockerMngImpl) SaveFile(ctx context.Context, path string, data []byte, stat *types.ContainerPathStat) (err error) {
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	defer writer.Close()

	dir, name := filepath.Split(path)
	hdr := &tar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    int64(stat.Mode),
		ModTime: time.Now(),
	}
	if err := writer.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := writer.Write(data); err != nil {
		return err
	}
	reader := tar.NewReader(bytes.NewReader(buffer.Bytes()))
	err = d.dockerClient.CopyToContainer(ctx, d.id, dir, reader, types.CopyToContainerOptions{})

	return
}
