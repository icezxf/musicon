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

func Parse(data []byte) (*Info, error) {
m, err := tag.ReadFrom(bytes.NewReader(data))
if err != nil {
return nil, err
}
info := &Info{
Title:       strings.TrimSpace(m.Title()),
Artist:      strings.TrimSpace(m.Artist()),
Album:       strings.TrimSpace(m.Album()),
AlbumArtist: strings.TrimSpace(m.AlbumArtist()),
Genre:       strings.TrimSpace(m.Genre()),
}
if pic := m.Picture(); pic != nil {
info.CoverData = pic.Data
info.CoverMime = pic.MIMEType
}
info.Lyrics = extractLyrics(m)
return info, nil
}

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

func RepairMojibake(s string) string {
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
out = append(out, LRCLine{Value: line})
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
