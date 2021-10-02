// Package get provides a "get" tool for downloading files.
package get

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"repos/pkg/repos"
)

const (
	unpackTmpFolder = ".unpack.tmp"
)

// Params defines the parameters in rule.
type Params struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	Digest    string `json:"digest"`
	UnpackTo  string `json:"unpack-to"`
	UseSubDir string `json:"use-subdir"`
}

// Tool defines the tool to be registered.
type Tool struct {
}

// Executor implements repos.ToolExecutor.
type Executor struct {
	URL          *url.URL
	Filename     string
	DigestAlgo   string
	DigestValue  string
	UnpackOutDir string
	UseSubDir    string

	digester func() hash.Hash
	unpacker func(ctx context.Context, xctx *repos.ToolExecContext, fn, dir string) *exec.Cmd
}

// CreateToolExecutor implements repos.Tool.
func (t *Tool) CreateToolExecutor(target *repos.Target) (repos.ToolExecutor, error) {
	var params Params
	if err := target.ToolParamsAs(&params); err != nil {
		return nil, fmt.Errorf("decode params error: %w", err)
	}
	if params.URL == "" {
		return nil, fmt.Errorf("missing parameter URL")
	}
	parsedURL, err := url.Parse(params.URL)
	if err != nil {
		return nil, fmt.Errorf("parse URL %q error: %w", params.URL, err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return nil, fmt.Errorf("unsupported URL scheme %q", parsedURL.Scheme)
	}
	digests := strings.SplitN(params.Digest, ":", 2)
	if len(digests) != 2 || digests[1] == "" {
		return nil, fmt.Errorf("invalid digest format: %q", params.Digest)
	}
	x := &Executor{
		URL:         parsedURL,
		Filename:    params.Filename,
		DigestAlgo:  strings.ToLower(digests[0]),
		DigestValue: digests[1],
	}
	if x.Filename == "" {
		x.Filename = filepath.Base(x.URL.EscapedPath())
	}
	if x.Filename == "" {
		return nil, fmt.Errorf("unable to infer filename from URL %q, please specify", params.URL)
	}
	switch x.DigestAlgo {
	case "sha1":
		x.digester = sha1.New
	case "sha256":
		x.digester = sha256.New
	case "sha512":
		x.digester = sha512.New
	case "md5":
		x.digester = md5.New
	default:
		return nil, fmt.Errorf("unsupported digest algorithm: %s", x.DigestAlgo)
	}

	if params.UnpackTo != "" {
		if params.UnpackTo == target.Name.GlobalName()+unpackTmpFolder {
			return nil, fmt.Errorf("illegal value of unpack-to %q", params.UnpackTo)
		}
		x.UnpackOutDir = params.UnpackTo
		x.UseSubDir = params.UseSubDir

		switch {
		case strings.HasSuffix(x.Filename, ".tar"):
			x.unpacker = tarUnpacker
		case strings.HasSuffix(x.Filename, ".tar.gz"):
			x.unpacker = tarGzUnpacker
		case strings.HasSuffix(x.Filename, ".tar.bz2"):
			x.unpacker = tarBz2Unpacker
		case strings.HasSuffix(x.Filename, ".tar.xz"):
			x.unpacker = tarXzUnpacker
		case strings.HasSuffix(x.Filename, ".zip"):
			x.unpacker = zipUnpacker
		default:
			return nil, fmt.Errorf("unknown how to unpack according to filename: %s", x.Filename)
		}
	}

	return x, nil
}

// Execute implements repos.ToolExecutor.
func (x *Executor) Execute(ctx context.Context, xctx *repos.ToolExecContext) error {
	cr := &repos.CacheReporter{Cache: repos.NewFilesCache(xctx)}
	cr.AddOutput("", x.Filename)
	cr.AddOpaque(x.DigestAlgo + ":" + x.DigestValue)
	if x.UnpackOutDir != "" {
		cr.AddOutputDir("dir", x.UnpackOutDir)
		cr.AddOpaque(x.UseSubDir)
	}
	if xctx.Skippable && cr.Verify() {
		xctx.Output(cr.SavedTaskOutputs())
		return repos.ErrSkipped
	}
	cr.ClearSaved()
	outFn := filepath.Join(xctx.OutDir, x.Filename)
	if !x.validateDigest(xctx) {
		os.Remove(outFn)
		downloadURL := x.URL.String()
		if err := xctx.RunAndLog(xctx.Command(ctx, "curl", "-fsSL", "-o", outFn, downloadURL)); err != nil {
			return fmt.Errorf("download %q error: %v", downloadURL, err)
		}
	}
	if x.unpacker != nil {
		unpackTmpDir := filepath.Join(xctx.OutDir, xctx.Task.Name()+unpackTmpFolder)
		unpackOutDir := filepath.Join(xctx.OutDir, x.UnpackOutDir)
		if x.UseSubDir == "" {
			unpackTmpDir = unpackOutDir
		}
		os.RemoveAll(unpackTmpDir)
		if err := os.MkdirAll(unpackTmpDir, 0755); err != nil {
			return fmt.Errorf("mkdir %q error: %v", unpackTmpDir, err)
		}
		cmd := x.unpacker(ctx, xctx, outFn, unpackTmpDir)
		cmd.Dir = unpackTmpDir
		if err := xctx.RunAndLog(cmd); err != nil {
			return fmt.Errorf("unpack %q error: %v", outFn, err)
		}
		if x.UseSubDir != "" {
			os.RemoveAll(unpackOutDir)
			fromDir := filepath.Join(unpackTmpDir, x.UseSubDir)
			if err := os.Rename(fromDir, unpackOutDir); err != nil {
				return fmt.Errorf("move %q to %q error: %v", fromDir, unpackOutDir, err)
			}
		}
	}
	xctx.PersistCacheOrLog(cr.Cache)
	xctx.Output(cr.Cache.TaskOutputs())
	return nil
}

func (x *Executor) validateDigest(xctx *repos.ToolExecContext) bool {
	outFn := filepath.Join(xctx.OutDir, x.Filename)
	f, err := os.Open(outFn)
	if err != nil {
		xctx.Logger.Printf("Verify digest of %q open error: %v", outFn, err)
		return false
	}
	defer f.Close()
	h := x.digester()
	if _, err := io.Copy(h, f); err != nil {
		xctx.Logger.Printf("Verify digest of %q read error: %v", outFn, err)
		return false
	}
	if val := hex.EncodeToString(h.Sum(nil)); val != x.DigestValue {
		xctx.Logger.Printf("Verify digest of %q inconsistent: %s vs %s (desired)", outFn, val, x.DigestValue)
		return false
	}
	return true
}

func tarUnpacker(ctx context.Context, xctx *repos.ToolExecContext, fn, dir string) *exec.Cmd {
	return xctx.Command(ctx, "tar", "-C", dir, "-xf", fn)
}

func tarGzUnpacker(ctx context.Context, xctx *repos.ToolExecContext, fn, dir string) *exec.Cmd {
	return xctx.Command(ctx, "tar", "-C", dir, "-zxf", fn)
}

func tarBz2Unpacker(ctx context.Context, xctx *repos.ToolExecContext, fn, dir string) *exec.Cmd {
	return xctx.Command(ctx, "tar", "-C", dir, "-jxf", fn)
}

func tarXzUnpacker(ctx context.Context, xctx *repos.ToolExecContext, fn, dir string) *exec.Cmd {
	return xctx.Command(ctx, "tar", "-C", dir, "-Jxf", fn)
}

func zipUnpacker(ctx context.Context, xctx *repos.ToolExecContext, fn, dir string) *exec.Cmd {
	return xctx.Command(ctx, "unzip", fn)
}

func init() {
	repos.RegisterTool("get", &Tool{})
}
