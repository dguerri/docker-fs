package dockerfs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/docker/docker/api/types"
	"github.com/plesk/docker-fs/lib/log"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

var _ = (fs.NodeGetattrer)((*Dir)(nil))
var _ = (fs.NodeLookuper)((*Dir)(nil))
var _ = (fs.NodeReaddirer)((*Dir)(nil))
var _ = (fs.NodeCreater)((*Dir)(nil))

type Dir struct {
	fs.Inode
	mng *Mng

	fullpath string
}

func (d *Dir) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) (err syscall.Errno) {
	defer log.Printf("[debug] Dir (%s) Getattr(): %v", d.fullpath, err)
	out.Owner.Uid = d.mng.uid
	out.Owner.Gid = d.mng.gid
	out.Mode = 0755
	return 0
}

func (d *Dir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (n *fs.Inode, syserr syscall.Errno) {
	defer log.Printf("[debug] Dir (%s) Lookup(%s): %v", d.fullpath, name, syserr)
	path := filepath.Join(d.fullpath, name)

	attrs, err := d.mng.docker.GetPathAttrs(ctx, path)
	if err != nil && strings.HasSuffix(err.Error(), "404") {
		return nil, syscall.ENOENT
	}
	if err != nil {
		log.Printf("[error] Failed to get raw attrs: %v, (%T)", err, err)
		return nil, syscall.EIO
	}
	mode := attrs.Mode
	log.Printf("[trace] (%s) Lookup(%s): mode = %o", d.fullpath, name, mode)

	out.Owner.Uid, out.Owner.Gid = d.mng.uid, d.mng.gid

	inode := d.mng.inodes.Inode(filepath.Clean(path))
	if (mode & os.ModeSymlink) != 0 {
		linkTarget := attrs.LinkTarget
		return d.NewPersistentInode(ctx, &fs.MemSymlink{Data: []byte(linkTarget)}, fs.StableAttr{Mode: fuse.S_IFLNK, Ino: inode}), 0
	}

	if mode.IsDir() {
		return d.NewPersistentInode(ctx, &Dir{mng: d.mng, fullpath: path}, fs.StableAttr{Mode: fuse.S_IFDIR, Ino: inode}), 0
	}

	return d.NewPersistentInode(ctx, &File{mng: d.mng, fullpath: path}, fs.StableAttr{Ino: inode}), 0
}

func (d *Dir) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (node *fs.Inode, fh fs.FileHandle, fuseFlags uint32, errno syscall.Errno) {
	defer log.Printf("[debug] Dir (%s) Create(%q, flags=%o, mode=%o, ...): %v", d.fullpath, name, flags, mode, errno)
	path := filepath.Join(d.fullpath, name)
	// check if file exist
	_, syserr := d.Lookup(ctx, name, &fuse.EntryOut{})
	if syserr == 0 {
		// file already exist
		log.Printf("[error] File %q already exist", path)
		errno = syscall.EEXIST
		return
	}
	if syserr != syscall.ENOENT {
		log.Printf("[error] Lookup %q failed: %v", path, syserr)
		errno = syserr
		return
	}

	f := &File{
		mng:      d.mng,
		fullpath: path,
		// Allow fsync on this file
		write: true,
		stat: &types.ContainerPathStat{
			Mode: os.FileMode(mode),
		},
	}

	inode := d.mng.inodes.Inode(filepath.Clean(path))

	node = d.NewPersistentInode(ctx, f, fs.StableAttr{Ino: inode})
	return
}

func (d *Dir) Readdir(ctx context.Context) (ds fs.DirStream, syserr syscall.Errno) {
	defer log.Printf("[debug] Dir (%s) Readdir(): %v", d.fullpath, syserr)
	children := make(map[string]uint32)
	path := d.fullpath
	if path != "/" {
		path = path + "/"
	}

	changes, err := d.mng.ChangesInDir(ctx, d.fullpath)
	if err != nil {
		log.Printf("[error] Cannot retrieve FS changes: %v", err)
		return nil, syscall.EIO
	}

	// check static files and removed ones
	for name, mode := range d.mng.staticFiles {
		if !strings.HasPrefix(name, path) {
			continue
		}
		// Check if file is removed
		if WasRemoved(name, changes) {
			continue
		}
		sub := name[len(path):]
		pos := strings.Index(sub, "/")
		if pos > 0 {
			log.Printf("[trace] Readdir (1): children[%v] = %o", sub[:pos], fuse.S_IFDIR)
			children[sub[:pos]] = fuse.S_IFDIR
		} else if pos < 0 {
			log.Printf("[trace] Readdir (2): children[%v] = %o", sub, uint32(mode))
			if (mode & fuse.S_IFLNK) == fuse.S_IFLNK {
				children[sub] = fuse.S_IFLNK
			} else {
				children[sub] = fuse.S_IFREG
			}
		}
	}

	// check added files
	for _, ch := range changes {
		if ch.Kind != FileAdded {
			continue
		}
		stat, err := d.mng.docker.GetPathAttrs(ctx, ch.Path)
		if err != nil {
			if err != nil && strings.HasSuffix(err.Error(), "404") {
				log.Printf("[error] Failed to get raw attrs of %q: %v", ch.Path, err)
			}
			continue
		}
		mode := uint32(stat.Mode)
		log.Printf("[trace] Readdir (3): childred[%v] = %o", filepath.Base(ch.Path), mode)
		fuseMode := uint32(fuse.S_IFREG)
		if os.FileMode(mode).IsDir() {
			fuseMode = fuse.S_IFDIR
		}
		children[filepath.Base(ch.Path)] = fuseMode
	}

	var list []fuse.DirEntry
	for child, mode := range children {
		inode := d.mng.inodes.Inode(filepath.Clean(filepath.Join(d.fullpath, child)))
		list = append(list, fuse.DirEntry{
			Mode: mode,
			Name: child,
			Ino:  inode,
		})
	}
	return fs.NewListDirStream(list), 0
}
