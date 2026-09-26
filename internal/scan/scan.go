package scan

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/icezxf/musicon-go/internal/alist"
	"github.com/icezxf/musicon-go/internal/db"
	"github.com/icezxf/musicon-go/internal/meta"
)

var audioExts = map[string]string{
	".mp3": "MP3", ".flac": "FLAC", ".m4a": "M4A", ".aac": "AAC",
	".ogg": "OGG", ".opus": "OPUS", ".wav": "WAV",
	".wma": "WMA", ".asf": "WMA",
}

var noTagFormats = map[string]bool{
	"WMA": true,
}

type Scanner struct {
	DB       *db.Holder
	AList    *alist.Manager
	CoverDir string
	WG       *sync.WaitGroup
}

// Run 扫描：providerID 决定用哪个 AList；sourceID 决定歌曲归属哪个音源
func (s *Scanner) Run(taskID, rootPath, providerID, sourceID string) {
	if s.WG != nil {
		s.WG.Add(1)
		defer s.WG.Done()
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[scan] panic: %v\n%s", r, debug.Stack())
			s.DB.SetTaskStatus(taskID, "failed")
		}
	}()
	if providerID == "" {
		providerID = "alist-1"
	}
	log.Printf("[scan] start %s path=%s provider=%s source=%s", taskID, rootPath, providerID, sourceID)
	total := 0
	processed := 0
	s.walk(taskID, rootPath, providerID, sourceID, &total, &processed)
	s.DB.SetTaskStatus(taskID, "done")
	log.Printf("[scan] done %s total=%d processed=%d", taskID, total, processed)
}

func (s *Scanner) walk(taskID, dir, providerID, sourceID string, total, processed *int) {
	client := s.AList.Get(providerID)
	entries, err := client.List(dir)
	if err != nil {
		log.Printf("[scan] list %s: %v", dir, err)
		return
	}
	for _, e := range entries {
		full := path.Join(dir, e.Name)
		if e.IsDir {
			s.walk(taskID, full, providerID, sourceID, total, processed)
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name))
		fmtName, ok := audioExts[ext]
		if !ok {
			continue
		}
		*total++
		if err := s.probeAndSave(client, full, providerID, sourceID, fmtName, e.Size); err != nil {
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

func (s *Scanner) probeAndSave(client *alist.Client, fullPath, providerID, sourceID, fmtName string, fileSize int64) error {
	rawURL, err := client.GetRawURL(fullPath)
	if err != nil {
		return err
	}
	filename := path.Base(fullPath)

	if noTagFormats[fmtName] {
		title := strings.TrimSuffix(filename, filepath.Ext(filename))
		artist := ""
		if m := splitFilename(filename); m != nil {
			artist, title = m[0], m[1]
		}
		log.Printf("[probe] %s ⊘ %s 无标签格式，直接入库 (title=%s artist=%s)",
			filename, fmtName, title, artist)
		return s.DB.UpsertSong(db.Song{
			ID:         stableID(fullPath),
			Title:      title,
			Artist:     artist,
			Fmt:        fmtName,
			Path:       fullPath,
			ProviderID: providerID,
			SourceID:   sourceID,
			FileSize:   fileSize,
		})
	}

	var data []byte
	var info *meta.Info
	sizes := []int64{
		1 * 1024 * 1024, 2 * 1024 * 1024, 4 * 1024 * 1024,
		8 * 1024 * 1024, 16 * 1024 * 1024,
	}
	for i, size := range sizes {
		data, err = client.ReadRange(rawURL, 0, size-1)
		if err != nil {
			return err
		}
		info, err = meta.Parse(data, filename)
		if err != nil || info == nil {
			info = &meta.Info{Title: filename}
		}
		hasCover := len(info.CoverData) > 0
		hasDur := info.Duration > 0
		hasLyrics := info.Lyrics != ""
		if hasCover && hasDur && hasLyrics {
			log.Printf("[probe] %s ✓ 封面=%d 时长=%ds 歌词=%d字节 (读 %dMB)",
				filename, len(info.CoverData), info.Duration, len(info.Lyrics), size/1024/1024)
			break
		}
		if hasCover && hasDur && i >= 1 {
			log.Printf("[probe] %s ✓ 封面=%d 时长=%ds (读 %dMB, 无歌词)",
				filename, len(info.CoverData), info.Duration, size/1024/1024)
			break
		}
		if hasCover && i >= 2 {
			log.Printf("[probe] %s ✓ 封面=%d (读 %dMB, 无时长无歌词)",
				filename, len(info.CoverData), size/1024/1024)
			break
		}
		if i == len(sizes)-1 {
			log.Printf("[probe] %s ⚠ 16MB 内未读全 (封面=%v 时长=%v 歌词=%v)",
				filename, hasCover, hasDur, hasLyrics)
		}
	}

	title := info.Title
	if title == "" {
		title = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	artist := info.Artist
	album := info.Album
	aa := info.AlbumArtist
	if aa == "" {
		aa = artist
	}
	coverPath := ""
	if len(info.CoverData) > 0 {
		coverPath = s.saveCover(info.CoverData, info.CoverMime)
	}

	return s.DB.UpsertSong(db.Song{
		ID:          stableID(fullPath),
		Title:       title,
		Artist:      artist,
		Album:       album,
		AlbumArtist: aa,
		Genre:       info.Genre,
		Fmt:         fmtName,
		Dur:         info.Duration,
		Path:        fullPath,
		ProviderID:  providerID,
		SourceID:    sourceID,
		CoverPath:   coverPath,
		Lyrics:      info.Lyrics,
		TrackNumber: info.TrackNumber,
		DiscNumber:  info.DiscNumber,
		Year:        info.Year,
		Composer:    info.Composer,
		Bitrate:     info.Bitrate,
		SampleRate:  info.SampleRate,
		Channels:    info.Channels,
		FileSize:    fileSize,
		ISRC:        info.ISRC,
		BPM:         info.BPM,
	})
}

func splitFilename(filename string) []string {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	for _, sep := range []string{" - ", " -", "- ", "-"} {
		if i := strings.Index(name, sep); i > 0 {
			a := strings.TrimSpace(name[:i])
			t := strings.TrimSpace(name[i+len(sep):])
			if a != "" && t != "" {
				return []string{a, t}
			}
		}
	}
	return nil
}

func (s *Scanner) saveCover(data []byte, mime string) string {
	if s.CoverDir == "" {
		return ""
	}
	os.MkdirAll(s.CoverDir, 0755)
	h := sha1.Sum(data)
	ext := ".jpg"
	switch strings.ToLower(mime) {
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}
	name := hex.EncodeToString(h[:]) + ext
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