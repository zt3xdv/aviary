package hostfs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	ErrNotFound        = errors.New("file not found")
	ErrIsDirectory     = errors.New("is a directory")
	ErrNotDirectory    = errors.New("not a directory")
	ErrPathContainment = errors.New("path escapes volume root")
)

// FileEntry represents a file/directory entry
type FileEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode"`
	ModTime string    `json:"mod_time"`
	IsDir   bool      `json:"is_dir"`
}

// HostFS manages container volumes on the host filesystem
type HostFS struct {
	root string // e.g., /var/lib/aviary/volumes
}

// NewHostFS creates a new HostFS instance
func NewHostFS(root string) (*HostFS, error) {
	if root == "" {
		root = "/var/lib/aviary/volumes"
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, fmt.Errorf("failed to create hostfs root: %w", err)
	}
	return &HostFS{root: root}, nil
}

// VolumePath returns the host path for a container's volume
func (h *HostFS) VolumePath(containerID string) string {
	return filepath.Join(h.root, containerID)
}

// EnsureVolume creates the volume directory for a container
func (h *HostFS) EnsureVolume(containerID string) error {
	return os.MkdirAll(h.VolumePath(containerID), 0755)
}

// RemoveVolume removes a container's volume directory
func (h *HostFS) RemoveVolume(containerID string) error {
	return os.RemoveAll(h.VolumePath(containerID))
}

// resolvePath resolves and validates a path within the container's volume
func (h *HostFS) resolvePath(containerID, path string) (string, error) {
	volumeRoot := h.VolumePath(containerID)

	// Clean the path
	path = filepath.Clean(path)
	if path == "." {
		path = ""
	}

	// Join with volume root
	fullPath := filepath.Join(volumeRoot, path)

	// Ensure the resolved path is within the volume root (containment check)
	rel, err := filepath.Rel(volumeRoot, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", ErrPathContainment
	}

	// Resolve symlinks and verify again (defense in depth)
	resolvedPath, err := filepath.EvalSymlinks(fullPath)
	if err != nil {
		// If symlink target doesn't exist, use the original path
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		resolvedPath = fullPath
	} else {
		// Verify the resolved path is still within volume root
		rel, err = filepath.Rel(volumeRoot, resolvedPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return "", ErrPathContainment
		}
	}
	fullPath = resolvedPath

	return fullPath, nil
}

// List lists files in a directory
func (h *HostFS) List(ctx context.Context, containerID, path string) ([]FileEntry, error) {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(fullPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	result := make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		result = append(result, FileEntry{
			Name:    e.Name(),
			Path:    filepath.Join(path, e.Name()),
			Size:    info.Size(),
			Mode:    info.Mode().String(),
			ModTime: info.ModTime().Format(time.RFC3339),
			IsDir:   e.IsDir(),
		})
	}

	// Sort: directories first, then alphabetically
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir // directories first
		}
		return result[i].Name < result[j].Name
	})

	return result, nil
}

// Stat returns file metadata
func (h *HostFS) Stat(ctx context.Context, containerID, path string) (*FileEntry, error) {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return nil, err
	}

	info, err := os.Lstat(fullPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	return &FileEntry{
		Name:    info.Name(),
		Path:    path,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().Format(time.RFC3339),
		IsDir:   info.IsDir(),
	}, nil
}

// Read reads a file content
func (h *HostFS) Read(ctx context.Context, containerID, path string) (io.ReadCloser, error) {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(fullPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNotFound
		}
		if errors.Is(err, fs.ErrPermission) {
			return nil, fmt.Errorf("permission denied")
		}
		return nil, err
	}

	// Verify it's not a directory
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, ErrIsDirectory
	}

	return f, nil
}

// Write writes a file (creates or truncates)
func (h *HostFS) Write(ctx context.Context, containerID, path string, content io.Reader, size int64) error {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(fullPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return err
	}

	// Create/truncate the file
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	// Stream the content
	if size > 0 {
		// Use LimitReader to prevent writing more than expected
		_, err = io.Copy(f, io.LimitReader(content, size))
	} else {
		_, err = io.Copy(f, content)
	}

	return err
}

// Delete deletes a file or directory
func (h *HostFS) Delete(ctx context.Context, containerID, path string) error {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return err
	}

	// Prevent deleting the volume root
	if fullPath == h.VolumePath(containerID) {
		return ErrPathContainment
	}

	return os.RemoveAll(fullPath)
}

// CreateDir creates a directory
func (h *HostFS) CreateDir(ctx context.Context, containerID, path string, mode uint32) error {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return err
	}

	return os.MkdirAll(fullPath, fs.FileMode(mode))
}

// Chmod changes file permissions
func (h *HostFS) Chmod(ctx context.Context, containerID, path string, mode uint32) error {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return err
	}

	return os.Chmod(fullPath, fs.FileMode(mode))
}

// Move moves/renames a file or directory
func (h *HostFS) Move(ctx context.Context, containerID, srcPath, destPath string) error {
	srcFull, err := h.resolvePath(containerID, srcPath)
	if err != nil {
		return err
	}

	destFull, err := h.resolvePath(containerID, destPath)
	if err != nil {
		return err
	}

	// Ensure destination parent exists
	destParent := filepath.Dir(destFull)
	if err := os.MkdirAll(destParent, 0755); err != nil {
		return err
	}

	return os.Rename(srcFull, destFull)
}

// Copy copies a file or directory
func (h *HostFS) Copy(ctx context.Context, containerID, srcPath, destPath string) error {
	srcFull, err := h.resolvePath(containerID, srcPath)
	if err != nil {
		return err
	}

	destFull, err := h.resolvePath(containerID, destPath)
	if err != nil {
		return err
	}

	srcInfo, err := os.Stat(srcFull)
	if err != nil {
		return err
	}

	if srcInfo.IsDir() {
		return h.copyDir(srcFull, destFull)
	}

	return h.copyFile(srcFull, destFull, srcInfo.Mode())
}

func (h *HostFS) copyFile(src, dest string, mode fs.FileMode) error {
	// Ensure destination parent exists
	destParent := filepath.Dir(dest)
	if err := os.MkdirAll(destParent, 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, srcFile)
	return err
}

func (h *HostFS) copyDir(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from src
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		if rel == "." {
			// Create destination directory
			return os.MkdirAll(dest, 0755)
		}

		destPath := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(destPath, info.Mode())
		}

		return h.copyFile(path, destPath, info.Mode())
	})
}

// Update is an alias for Write (for text editor semantics)
func (h *HostFS) Update(ctx context.Context, containerID, path string, content io.Reader, size int64) error {
	return h.Write(ctx, containerID, path, content, size)
}

// DiskUsage returns disk usage for a container's volume
func (h *HostFS) DiskUsage(ctx context.Context, containerID string) (int64, error) {
	volumePath := h.VolumePath(containerID)
	var total int64

	err := filepath.WalkDir(volumePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() {
			total += info.Size()
		}
		return nil
	})

	return total, err
}

// SetQuota sets a disk quota (soft limit tracking, not enforced by kernel)
func (h *HostFS) SetQuota(containerID string, limitMB int) error {
	// Store quota in a metadata file
	quotaPath := filepath.Join(h.VolumePath(containerID), ".aviary_quota")
	data := fmt.Sprintf("%d\n", limitMB*1024*1024)
	return os.WriteFile(quotaPath, []byte(data), 0644)
}

// GetQuota returns the disk quota limit
func (h *HostFS) GetQuota(containerID string) (int64, error) {
	quotaPath := filepath.Join(h.VolumePath(containerID), ".aviary_quota")
	data, err := os.ReadFile(quotaPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil // No quota set
		}
		return 0, err
	}
	limit, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0, err
	}
	return limit, nil
}

// Chown changes file ownership (requires root)
func (h *HostFS) Chown(ctx context.Context, containerID, path string, uid, gid int) error {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return err
	}

	return os.Lchown(fullPath, uid, gid)
}

// EnsureDirOwnership recursively ensures ownership (for initialization)
func (h *HostFS) EnsureDirOwnership(containerID string, uid, gid int) error {
	volumePath := h.VolumePath(containerID)
	return filepath.WalkDir(volumePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}

// Symlink creates a symbolic link
func (h *HostFS) Symlink(ctx context.Context, containerID, target, linkPath string) error {
	fullLinkPath, err := h.resolvePath(containerID, linkPath)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	parentDir := filepath.Dir(fullLinkPath)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return err
	}

	return os.Symlink(target, fullLinkPath)
}

// Readlink reads the target of a symbolic link
func (h *HostFS) Readlink(ctx context.Context, containerID, path string) (string, error) {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return "", err
	}

	return os.Readlink(fullPath)
}

// Chtimes changes access and modification times
func (h *HostFS) Chtimes(ctx context.Context, containerID, path string, atime, mtime time.Time) error {
	fullPath, err := h.resolvePath(containerID, path)
	if err != nil {
		return err
	}

	return os.Chtimes(fullPath, atime, mtime)
}

// StatFS returns filesystem statistics for the volume
func (h *HostFS) StatFS(ctx context.Context, containerID string) (*syscall.Statfs_t, error) {
	volumePath := h.VolumePath(containerID)
	var stat syscall.Statfs_t
	err := syscall.Statfs(volumePath, &stat)
	return &stat, err
}