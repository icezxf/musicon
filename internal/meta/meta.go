package meta

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/dhowden/tag"
	"github.com/mewkiz/flac"
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

	TrackNumber int
	DiscNumber  int
	Year        int
	Composer    string
	Bitrate     int
	SampleRate  int
	Channels    int
	ISRC        string
	BPM         int
}

func Parse(data []byte, filename string) (*Info, error) {
	info := &Info{}

	// 1) dhowden/tag 标签
	if m, err := tag.ReadFrom(bytes.NewReader(data)); err == nil {
		info.Title = strings.TrimSpace(m.Title())
		info.Artist = strings.TrimSpace(m.Artist())
		info.Album = strings.TrimSpace(m.Album())
		info.AlbumArtist = strings.TrimSpace(m.AlbumArtist())
		info.Genre = strings.TrimSpace(m.Genre())
		info.Composer = strings.TrimSpace(m.Composer())
		info.Year = m.Year()
		if n, _ := m.Track(); n > 0 {
			info.TrackNumber = n
		}
		if n, _ := m.Disc(); n > 0 {
			info.DiscNumber = n
		}
		if pic := m.Picture(); pic != nil {
			info.CoverData = pic.Data
			info.CoverMime = pic.MIMEType
		}
		info.Lyrics = extractLyrics(m)

		// 从 Raw 里读更多
		if raw := m.Raw(); raw != nil {
			for _, key := range []string{"ARTIST", "TPE1"} {
				if v, ok := raw[key]; ok {
					if multi := extractMulti(v); multi != "" {
						info.Artist = multi
						break
					}
				}
			}
			for _, key := range []string{"ALBUMARTIST", "TPE2"} {
				if v, ok := raw[key]; ok {
					if multi := extractMulti(v); multi != "" {
						info.AlbumArtist = multi
						break
					}
				}
			}
			for _, key := range []string{"ISRC", "TSRC"} {
				if v, ok := raw[key]; ok {
					if s := toStr(v); s != "" {
						info.ISRC = s
						break
					}
				}
			}
			for _, key := range []string{"BPM", "TBPM"} {
				if v, ok := raw[key]; ok {
					if s := toStr(v); s != "" {
						info.BPM = atoi(s)
						break
					}
				}
			}
		}
	}

	// 1.5) M4A 手工补全：dhowden/tag 读不到 ©lyr 和时长，手动解析
	if len(data) >= 12 {
		header := string(data[4:8])
		if header == "ftyp" || header == "moov" {
			if info.Lyrics == "" {
				info.Lyrics = extractM4ALyrics(data)
			}
			if info.Duration == 0 {
				info.Duration = extractM4ADuration(data)
			}
		}
	}

	// 2) FLAC 手工补全 + 音频流信息
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
		if info.TrackNumber == 0 {
			info.TrackNumber = atoi(firstVC(vc, "TRACKNUMBER", "TRACK"))
		}
		if info.DiscNumber == 0 {
			info.DiscNumber = atoi(firstVC(vc, "DISCNUMBER", "DISC"))
		}
		if info.Year == 0 {
			info.Year = atoi(firstVC(vc, "DATE", "YEAR"))
		}
		if info.Composer == "" {
			info.Composer = firstVC(vc, "COMPOSER")
		}
		if info.ISRC == "" {
			info.ISRC = firstVC(vc, "ISRC")
		}
		if info.BPM == 0 {
			info.BPM = atoi(firstVC(vc, "BPM"))
		}

		if stream, err := flac.Parse(bytes.NewReader(data)); err == nil && stream != nil {
			sr := int(stream.Info.SampleRate)
			ch := int(stream.Info.NChannels)
			bps := int(stream.Info.BitsPerSample)
			if sr > 0 {
				info.SampleRate = sr
			}
			if ch > 0 {
				info.Channels = ch
			}
			if sr > 0 && ch > 0 && bps > 0 {
				info.Bitrate = sr * ch * bps / 1000
			}
			if info.Duration == 0 && sr > 0 && stream.Info.NSamples > 0 {
				info.Duration = int(stream.Info.NSamples / uint64(sr))
			}
			stream.Close()
		}
	}

	// 3) 文件名 fallback
	if info.Title == "" {
		info.Title = titleFromFilename(filename)
	}
	if info.Artist == "" {
		info.Artist, info.Title = artistFromFilename(filename, info.Title)
	}

	info.Title = repairMojibake(info.Title)
	info.Artist = repairMojibake(info.Artist)
	info.Album = repairMojibake(info.Album)
	info.AlbumArtist = repairMojibake(info.AlbumArtist)
	info.Composer = repairMojibake(info.Composer)

	return info, nil
}

// extractM4ALyrics 直接从 MP4/M4A 原始字节提取 ©lyr 歌词
func extractM4ALyrics(data []byte) string {
	marker := []byte{0xA9, 'l', 'y', 'r'}
	idx := bytes.Index(data, marker)
	if idx < 0 {
		marker = []byte{0xA9, 'L', 'Y', 'R'}
		idx = bytes.Index(data, marker)
		if idx < 0 {
			return ""
		}
	}
	pos := idx + 4
	if pos+8 > len(data) {
		return ""
	}
	if string(data[pos+4:pos+8]) != "data" {
		return ""
	}
	dataSize := int(data[pos])<<24 | int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
	if dataSize < 16 || pos+dataSize > len(data) {
		return ""
	}
	textStart := pos + 16
	textEnd := pos + dataSize
	if textStart >= textEnd {
		return ""
	}
	return strings.TrimSpace(string(data[textStart:textEnd]))
}

// extractM4ADuration 解析 MP4 的 mvhd atom 拿时长（秒）
func extractM4ADuration(data []byte) int {
	idx := bytes.Index(data, []byte("mvhd"))
	if idx < 0 {
		return 0
	}
	pos := idx + 4
	if pos+20 > len(data) {
		return 0
	}
	version := data[pos]
	pos += 4 // skip version + flags

	if version == 1 {
		if pos+28 > len(data) {
			return 0
		}
		pos += 16
		timescale := int(data[pos])<<24 | int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4
		if pos+8 > len(data) {
			return 0
		}
		// 64位 duration
		durHi := uint64(data[pos])<<56 | uint64(data[pos+1])<<48 | uint64(data[pos+2])<<40 | uint64(data[pos+3])<<32
		durLo := uint64(data[pos+4])<<24 | uint64(data[pos+5])<<16 | uint64(data[pos+6])<<8 | uint64(data[pos+7])
		dur := int64(durHi | durLo)
		if timescale > 0 {
			return int(dur / int64(timescale))
		}
	} else {
		if pos+16 > len(data) {
			return 0
		}
		pos += 8
		timescale := int(data[pos])<<24 | int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4
		dur := int(data[pos])<<24 | int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		if timescale > 0 {
			return dur / timescale
		}
	}
	return 0
}

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

func extractLyrics(m tag.Metadata) string {
	if lyr := strings.TrimSpace(m.Lyrics()); lyr != "" {
		return lyr
	}
	raw := m.Raw()
	if raw == nil {
		return ""
	}
	for _, k := range []string{
		"LYRICS", "UNSYNCEDLYRICS", "UNSYNCED LYRICS", "LYRIC",
		"©lyr", "©LYR", "\xA9lyr", "\xA9LYR",
	} {
		if v, ok := raw[k]; ok {
			if s := toStr(v); s != "" {
				return s
			}
		}
	}
	for k, v := range raw {
		ku := strings.ToUpper(k)
		if strings.Contains(ku, "LYRIC") ||
			strings.HasPrefix(ku, "USLT") ||
			strings.HasSuffix(ku, "LYR") ||
			strings.Contains(ku, "\xA9LYR") {
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
