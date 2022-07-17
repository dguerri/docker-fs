package dockerfs

import (
	"github.com/docker/docker/api/types/container"
)

func WasRemoved(path string, c []container.ContainerChangeResponseItem) bool {
	for _, ch := range c {
		if ch.Path == path && ch.Kind == FileRemoved {
			return true
		}
	}
	return false
}

const (
	FileModified uint8 = 0
	FileAdded    uint8 = 1
	FileRemoved  uint8 = 2
)
