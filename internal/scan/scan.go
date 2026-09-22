package scan

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/icezxf/musicon-go/internal/alist"
	"github.com/icezxf/musicon-go/internal/db"
	"github.com/icezxf/musicon-go/internal/meta"
)

var audioExts = map[string]string{
	".mp3": "MP3", ".flac": "FLAC", ".m4a": "M4A", ".aac": "AAC",
	".ogg": "OGG", ".opus": "OPUS", ".wav": "WAV",
}

type Scanner struct {
	DB       *db.Holder
	AList    *alist.Client
	CoverDir string
}

func (s *Scanner) Run(taskID, rootPath, providerID string) {
	log.Printf("[scan] start %s %s", taskID, rootPath)
	total := 0
	processed := 0
	s.walk(taskID, rootPath, providerID, &total, &processed)
	s.DB.SetTaskStatus(taskID, "done")
	log.Printf("[scan] done %s total=%d processed=%d", taskID, total, processed)
}

func (s *Scanner) walk(taskID, dir, providerID string, total, processed *int) {
	entries, err := s.AList.List(dir)
	if err != nil {
		log.Printf("[scan] list %s: %v", dir, err)
		return
	}
	for _, e := range entries {
		full := path.Join(dir, e.Name)
		if e.IsDir {
			s.walk(taskID, full, providerID, total, processed)
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name))
		fmtName, ok := audioExts[ext]
		if !ok {
			continue
		}
		*total++
		if err := s.probeAndSave(full, providerID, fmtName); err != nil {
			log.Printf("[scan] probe %s: %v", full, err)
			continue
		}
		*processed++
		if *total%10 == 0 {
			s.DB.SetTaskProgress(taskID, *total, *processed)
		}
	}
	s.DB.SetTaskProgress(taskID, *total, *processed)
}

func (s *Scanner) probeAndSave(fullPath, providerID, fmtName string) error {
	rawURL, err := s.AList.GetRawURL(fullPath)
	if err != nil {
		return err
	}

	filename := path.Base(fullPath)

	// 渐进式读取：1MB → 2MB → 4MB → 8MB
	// 一旦解析出封面就停，避免对没封面的文件浪费带宽
	var data []byte
	var info *meta.Info
	sizes := []int64{1 * 1024 * 1024, 2 * 1024 * 1024, 4 * 1024 * 1024, 8 * 1024 * 1024}

	for i, size := range sizes {
		data, err = s.AList.ReadRange(rawURL, 0, size-1)
		if err != nil {
			return err
		}
		info, err = meta.Parse(data, filename)
		if err != nil || info == nil {
			info = &meta.Info{Title: filename}
		}
		// 有封面就停
		if len(info.CoverData) > 0 {
			log.Printf("[probe] %s ✓ 读到封面 (%d 字节, 共读 %dMB)",
				filename, len(info.CoverData), size/1024/1024)
			break
		}
		// 最后一次还没读到封面，就用这次的
		if i == len(sizes)-1 {
			log.Printf("[probe] %s ✗ 8MB 内无封面", filename)
		}
	}

func (s *Scanner) saveCover(data []byte) string {
	if s.CoverDir == "" {
		return ""
	}
	os.MkdirAll(s.CoverDir, 0755)
	h := sha1.Sum(data)
	name := hex.EncodeToString(h[:]) + ".jpg"
	p := filepath.Join(s.CoverDir, name)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		return ""
	}
	return p
}

func stableID(p string) string {
	h := sha1.Sum([]byte(p))
	return "go-" + hex.EncodeToString(h[:8])
}

var _ = log.Printf
