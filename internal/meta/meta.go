package meta

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/dhowden/tag"
)

type Info struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	Genre       string
	Duration    int
	Lyrics      string
	CoverData   []byte
	CoverMime   string
}

func Parse(data []byte, filename string) (*Info, error) {
	info := &Info{}

	if m, err := tag.ReadFrom(bytes.NewReader(data)); err == nil {
		info.Title = strings.TrimSpace(m.Title())
		info.Artist = strings.TrimSpace(m.Artist())
		info.Album = strings.TrimSpace(m.Album())
		info.AlbumArtist = strings.TrimSpace(m.AlbumArtist())
		info.Genre = strings.TrimSpace(m.Genre())

		if pic := m.Picture(); pic != nil {
			info.CoverData = pic.Data
			info.CoverMime = pic.MIMEType
		}
		info.Lyrics = extractLyrics(m)

		// 从 Raw 里读所有艺术家（FLAC 多 ARTIST / MP3 多 TPE1）
		if raw := m.Raw(); raw != nil {
			for _, key := range []string{"ARTIST", "TPE1", "albumartist", "TPE2"} {
				if v, ok := raw[key]; ok {
					multi := extractMulti(v)
					if multi != "" {
						if key == "ARTIST" || key == "TPE1" {
							info.Artist = multi
						} else if key == "albumartist" || key == "TPE2" {
							info.AlbumArtist = multi
						}
						break
					}
				}
			}
		}
	}

	// FLAC 手工补全
	if len(data) >= 4 && string(data[:4]) == "fLaC" {
		vc := parseFLACVorbis(data)
		if info.Title == "" {
			info.Title = firstVC(vc, "TITLE")
		}
		if info.Artist == "" {
			info.Artist = allVC(vc, "ARTIST")
		}
		if info.Album == "" {
			info.Album = firstVC(vc, "ALBUM")
		}
		if info.AlbumArtist == "" {
			info.AlbumArtist = allVC(vc, "ALBUMARTIST", "ALBUM_ARTIST")
		}
		if info.Genre == "" {
			info.Genre = firstVC(vc, "GENRE")
		}
		if info.Lyrics == "" {
			info.Lyrics = firstVC(vc, "LYRICS", "UNSYNCEDLYRICS", "UNSYNCED LYRICS")
		}
		if info.Duration == 0 {
			info.Duration = flacDuration(data)
		}
	}

	// 文件名 fallback
	if info.Title == "" {
		info.Title = titleFromFilename(filename)
	}
	if info.Artist == "" {
		info.Artist, info.Title = artistFromFilename(filename, info.Title)
	}

	// 修复乱码
	info.Title = repairMojibake(info.Title)
	info.Artist = repairMojibake(info.Artist)
	info.Album = repairMojibake(info.Album)
	info.AlbumArtist = repairMojibake(info.AlbumArtist)

	return info, nil
}

// extractMulti 把多值元数据合并
func extractMulti(v any) string {
	switch x := v.(type) {
	case []string:
		var out []string
		for _, s := range x {
			s = strings.TrimSpace(s)
			if s != "" && !containsStr(out, s) {
				out = append(out, s)
			}
		}
		return strings.Join(out, "、")
	case string:
		return strings.TrimSpace(x)
	}
	return ""
}

func containsStr(arr []string, s string) bool {
	for _, x := range arr {
		if x == s {
			return true
		}
	}
	return false
}

// ---------- FLAC 手工解析 ----------

func parseFLACVorbis(data []byte) map[string][]string {
	if len(data) < 4 || string(data[:4]) != "fLaC" {
		return nil
	}
	out := map[string][]string{}
	pos := 4
	for pos+4 <= len(data) {
		header := data[pos]
		isLast := header&0x80 != 0
		blockType := header & 0x7f
		blockLen := int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		if pos+4+blockLen > len(data) {
			break
		}
		if blockType == 4 {
			comment := data[pos+4 : pos+4+blockLen]
			parseVorbisComment(comment, out)
			return out
		}
		if isLast {
			break
		}
		pos += 4 + blockLen
	}
	return out
}

func parseVorbisComment(comment []byte, out map[string][]string) {
	p := 0
	if p+4 > len(comment) {
		return
	}
	vendorLen := le32(comment[p : p+4])
	p += 4 + vendorLen
	if p+4 > len(comment) {
		return
	}
	count := le32(comment[p : p+4])
	p += 4
	for i := 0; i < count && p+4 <= len(comment); i++ {
		l := le32(comment[p : p+4])
		p += 4
		if p+l > len(comment) {
			break
		}
		kv := string(comment[p : p+l])
		p += l
		if eq := strings.IndexByte(kv, '='); eq > 0 {
			k := strings.ToUpper(strings.TrimSpace(kv[:eq]))
			v := strings.TrimSpace(kv[eq+1:])
			if v != "" {
				out[k] = append(out[k], v)
			}
		}
	}
}

func le32(b []byte) int {
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24
}

func firstVC(vc map[string][]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := vc[k]; ok && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

// allVC 把多个值合并（FLAC 里多艺术家）
func allVC(vc map[string][]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := vc[k]; ok && len(v) > 0 {
			var out []string
			for _, s := range v {
				s = strings.TrimSpace(s)
				if s != "" && !containsStr(out, s) {
					out = append(out, s)
				}
			}
			return strings.Join(out, "、")
		}
	}
	return ""
}

// flacDuration 用 mewkiz/flac 读 STREAMINFO
func flacDuration(data []byte) int {
	defer func() { recover() }()
	stream, err := flacParseHelper(data)
	if err != nil || stream == 0 {
		return 0
	}
	return stream
}

// ---------- 文件名 fallback ----------

var reArtistTitle = regexp.MustCompile(`^(.+?)\s*-\s*(.+)$`)

func titleFromFilename(fn string) string {
	if fn == "" {
		return ""
	}
	if i := strings.LastIndexByte(fn, '.'); i > 0 {
		fn = fn[:i]
	}
	return strings.TrimSpace(fn)
}

func artistFromFilename(fn, currentTitle string) (string, string) {
	if fn == "" {
		return "", currentTitle
	}
	name := fn
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		name = name[:i]
	}
	if m := reArtistTitle.FindStringSubmatch(name); m != nil {
		a := strings.TrimSpace(m[1])
		t := strings.TrimSpace(m[2])
		if a != "" && t != "" {
			if currentTitle == "" || currentTitle == name {
				return a, t
			}
			return a, currentTitle
		}
	}
	return "", currentTitle
}

// ---------- 歌词 & 乱码 ----------

func extractLyrics(m tag.Metadata) string {
	raw := m.Raw()
	if raw == nil {
		return ""
	}
	for _, k := range []string{"LYRICS", "UNSYNCEDLYRICS", "UNSYNCED LYRICS", "LYRIC"} {
		if v, ok := raw[k]; ok {
			if s := toStr(v); s != "" {
				return s
			}
		}
	}
	for k, v := range raw {
		ku := strings.ToUpper(k)
		if strings.Contains(ku, "LYRIC") || strings.HasPrefix(ku, "USLT") {
			if s := toStr(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func toStr(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case []string:
		return strings.TrimSpace(strings.Join(x, "\n"))
	case []byte:
		return strings.TrimSpace(string(x))
	}
	return ""
}

func repairMojibake(s string) string {
	if s == "" {
		return s
	}
	if strings.ContainsRune(s, '\uFFFD') {
		b := make([]byte, 0, len(s))
		for _, r := range s {
			if r < 256 {
				b = append(b, byte(r))
			}
		}
		return string(b)
	}
	return s
}

// ---------- LRC ----------

type LRCLine struct {
	Start int    `json:"start,omitempty"`
	Value string `json:"value"`
}

var lrcRe = regexp.MustCompile(`\[(\d{1,3}):(\d{1,2})(?:\.(\d{1,3}))?\]`)

func ParseLRC(s string) []LRCLine {
	var out []LRCLine
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		ms := lrcRe.FindAllStringSubmatchIndex(line, -1)
		if len(ms) == 0 {
			out = append(out, LRCLine{Start: -1, Value: line})
			continue
		}
		text := strings.TrimSpace(lrcRe.ReplaceAllString(line, ""))
		if text == "" {
			continue
		}
		for _, m := range ms {
			mm := atoi(line[m[2]:m[3]])
			ss := atoi(line[m[4]:m[5]])
			frac := "0"
			if m[6] != -1 {
				frac = line[m[6]:m[7]]
			}
			for len(frac) < 3 {
				frac += "0"
			}
			ms3 := atoi(frac[:3])
			start := (mm*60+ss)*1000 + ms3
			out = append(out, LRCLine{Start: start, Value: text})
		}
	}
	return out
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
