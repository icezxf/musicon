package scan

import (
"crypto/sha1"
"encoding/hex"
"fmt"
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
data, err := s.AList.ReadRange(rawURL, 0, 1024*1024-1)
if err != nil {
return err
}
info, err := meta.Parse(data)
if err != nil {
info = &meta.Info{}
}

title := info.Title
if title == "" {
title = strings.TrimSuffix(path.Base(fullPath), filepath.Ext(fullPath))
}
artist := info.Artist
album := info.Album
aa := info.AlbumArtist
if aa == "" {
aa = artist
}

coverPath := ""
if len(info.CoverData) > 0 {
coverPath = s.saveCover(info.CoverData)
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
CoverPath:   coverPath,
Lyrics:      info.Lyrics,
})
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

var _ = fmt.Sprintf
