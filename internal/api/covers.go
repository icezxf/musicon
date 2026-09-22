package api

import (
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
)

// GET /api/covers/thumb/{size}/{filename}
// 真实缩放并缓存
func (h *Handler) coverThumb(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/covers/thumb/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "bad path", 400)
		return
	}
	size, err := strconv.Atoi(parts[0])
	if err != nil || size <= 0 || size > 2000 {
		size = 96
	}
	filename := parts[1]

	// 安全校验
	for _, c := range filename {
		if !(c == '.' || c == '-' || c == '_' ||
			(c >= '0' && c <= '9') ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z')) {
			http.Error(w, "bad filename", 400)
			return
		}
	}

	srcPath := filepath.Join(h.Cfg.DataDir, "covers", filename)
	if _, err := os.Stat(srcPath); err != nil {
		http.Error(w, "not found", 404)
		return
	}

	// 缩略图缓存
	thumbDir := filepath.Join(h.Cfg.DataDir, "covers_thumb")
	os.MkdirAll(thumbDir, 0755)
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	thumbPath := filepath.Join(thumbDir, fmt.Sprintf("%s_%d.jpg", base, size))

	// 命中缓存（且比原图新）
	if fi, err := os.Stat(thumbPath); err == nil && fi.Size() > 0 {
		http.ServeFile(w, r, thumbPath)
		return
	}

	f, err := os.Open(srcPath)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		// 解码失败，退回原文件
		http.ServeFile(w, r, srcPath)
		return
	}

	thumb := imaging.Fill(src, size, size, imaging.Center, imaging.Lanczos)
	if err := imaging.Save(thumb, thumbPath, imaging.JPEGQuality(85)); err != nil {
		http.Error(w, "save failed", 500)
		return
	}
	http.ServeFile(w, r, thumbPath)
}