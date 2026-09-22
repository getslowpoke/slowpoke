package agent

import (
	"regexp"
	"strings"
	"time"
)

var (
	fpmHeaderRe = regexp.MustCompile(`^\[(\d{2}-\w{3}-\d{4} \d{2}:\d{2}:\d{2})\]\s+\[pool (\S+)\]`)
	fpmFrameRe  = regexp.MustCompile(`^\[0x[0-9a-f]+\] (.+)$`)
	// "/var/www/releases/1134/app/Services/X.php:88" -> "app/Services/X.php:88"
	nestedProjectDirRe = regexp.MustCompile(`\s(?:app|src|lib|vendor|routes|database|config|public)/((?:app|src|lib|vendor|routes|database|config|public)/\S+)$`)
	projectPathRe      = regexp.MustCompile(`\s/\S*?/((?:app|src|lib|vendor|routes|database|config|public)/\S+)$`)
)

// fpmParser reads the php-fpm slowlog: header with the time, then the stack
// from the deepest frame up.
type fpmParser struct {
	src     Source
	agg     *Aggregator
	loc     *time.Location
	when    time.Time
	frames  []string
	inEntry bool
}

func NewFpmParser(src Source, agg *Aggregator) LineParser {
	loc := time.Local
	if src.Timezone != "" {
		if l, err := time.LoadLocation(src.Timezone); err == nil {
			loc = l
		}
	}
	return &fpmParser{src: src, agg: agg, loc: loc}
}

func (p *fpmParser) Feed(line string) {
	if m := fpmHeaderRe.FindStringSubmatch(line); m != nil {
		p.flush()
		when, err := time.ParseInLocation("02-Jan-2006 15:04:05", m[1], p.loc)
		if err != nil {
			return
		}
		p.when, p.inEntry, p.frames = when, true, nil
		return
	}
	if !p.inEntry {
		return
	}
	if strings.TrimSpace(line) == "" {
		p.flush()
		return
	}
	if m := fpmFrameRe.FindStringSubmatch(line); m != nil {
		p.frames = append(p.frames, relativeFrame(m[1]))
	}
}

func relativeFrame(frame string) string {
	frame = projectPathRe.ReplaceAllString(frame, " $1")
	// The shortest match stops at the deploy folder when it is named like a project
	// directory: "/var/www/app/routes/web.php" gives "app/routes/web.php".
	return nestedProjectDirRe.ReplaceAllString(frame, " $1")
}

func (p *fpmParser) Flush() { p.flush() }

func (p *fpmParser) flush() {
	if !p.inEntry {
		return
	}
	p.inEntry = false
	deepest := "(empty stack)"
	if len(p.frames) > 0 {
		deepest = p.frames[0]
	}
	code := ""
	for _, f := range p.frames {
		if i := strings.LastIndex(f, " "); i >= 0 && !thirdPartyCode(f[i+1:]) && strings.Contains(f[i+1:], "/") && !strings.HasPrefix(f[i+1:], "public/") {
			code = f[i+1:]
			break
		}
	}
	p.agg.AddPHPStall("php-fpm:"+p.src.Name, p.when, deepest, code)
}
