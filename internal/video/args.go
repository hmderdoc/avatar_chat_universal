package video

import (
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Demuxers ffprobe and ffmpeg may use. mov covers mp4/m4v/mov, matroska
// covers mkv/webm. Anything else (hls, concat, image2, ...) is refused, so
// a file cannot name other files or URLs for ffmpeg to open.
const demuxerWhitelist = "mov,matroska"

// FPS is the frame rate videos are scaled to: the shell's cell ceiling.
const FPS = 12

// AudioRate is the mono PCM rate ffmpeg resamples audio to.
const AudioRate = 22050

// localInput spells a local path so ffmpeg can only read it as a file.
func localInput(path string) string { return "file:" + path }

// YtDlpArgs is the yt-dlp argv (after the binary) that downloads one
// allowlisted page link into outTemplate. The URL comes last, after "--",
// so nothing in it is read as an option.
func YtDlpArgs(c Config, rawURL, outTemplate string) []string {
	maxMB := c.MaxBytes >> 20
	if maxMB <= 0 {
		maxMB = DefaultMaxMB
	}
	args := []string{
		"--ignore-config", "--no-plugin-dirs", "--no-cache-dir",
		"--use-extractors", strings.Join(c.Extractors, ","),
		"--no-playlist", "--max-downloads", "1",
		"--no-exec", "--no-cookies", "--no-cookies-from-browser", "--no-js-runtimes",
		"--socket-timeout", "15",
		"--max-filesize", strconv.FormatInt(maxMB, 10) + "M",
		"-f", "b[height<=480][ext=mp4]/b[height<=480]/w",
		"--no-part", "--no-mtime", "--no-write-thumbnail",
		"--quiet", "--no-warnings", "--no-progress",
		"-o", outTemplate,
	}
	if c.FFmpeg != "" {
		args = append(args, "--ffmpeg-location", c.FFmpeg)
	}
	return append(args, "--", rawURL)
}

// ProbeArgs is the ffprobe argv for a downloaded file.
func ProbeArgs(path string) []string {
	return []string{
		"-v", "error",
		"-protocol_whitelist", "file",
		"-format_whitelist", demuxerWhitelist,
		"-show_entries", "stream=codec_type",
		"-show_entries", "format=format_name,duration",
		"-of", "csv",
		localInput(path),
	}
}

// Probe is what ffprobe said about a file.
type Probe struct {
	Demuxer  string // "mov" or "matroska"
	HasVideo bool
	HasAudio bool
	Seconds  float64
}

// ErrNotVideo means the file is not a video in an allowed container.
var ErrNotVideo = errors.New("video: not a playable video (mp4, mov, mkv or webm)")

// ParseProbe reads ProbeArgs' csv output.
func ParseProbe(out []byte) (Probe, error) {
	var p Probe
	r := csv.NewReader(strings.NewReader(string(out)))
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return p, fmt.Errorf("video: ffprobe output: %w", err)
	}
	for _, rec := range recs {
		if len(rec) < 2 {
			continue
		}
		switch rec[0] {
		case "stream":
			switch rec[1] {
			case "video":
				p.HasVideo = true
			case "audio":
				p.HasAudio = true
			}
		case "format":
			for _, name := range strings.Split(rec[1], ",") {
				switch name {
				case "mov", "matroska":
					p.Demuxer = name
				}
			}
			if len(rec) >= 3 {
				if s, err := strconv.ParseFloat(rec[2], 64); err == nil && s > 0 {
					p.Seconds = s
				}
			}
		}
	}
	if p.Demuxer == "" || !p.HasVideo {
		return p, ErrNotVideo
	}
	return p, nil
}

// PlayArgs is the ffmpeg argv that plays a probed local file in real time:
// RGB frames of cols x 2*rows pixels (the TV lounge's frame shape) on
// stdout, and, with audio, mono PCM on fd 3.
func PlayArgs(path string, p Probe, cols, rows, maxSeconds int, audio bool) []string {
	w, h := cols, 2*rows
	if maxSeconds <= 0 {
		maxSeconds = DefaultMaxSeconds
	}
	filter := fmt.Sprintf("fps=%d,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black",
		FPS, w, h, w, h)
	args := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error",
		"-threads", "2",
		"-protocol_whitelist", "file",
		"-format_whitelist", demuxerWhitelist,
		"-f", p.Demuxer,
		"-re", "-t", strconv.Itoa(maxSeconds),
		"-i", localInput(path),
		"-map", "0:v:0", "-vf", filter,
		"-f", "rawvideo", "-pix_fmt", "rgb24", "pipe:1",
	}
	if audio && p.HasAudio {
		args = append(args, "-map", "0:a:0", "-f", "s16le", "-ar", strconv.Itoa(AudioRate), "-ac", "1",
			"-flush_packets", "1", "pipe:3")
	}
	return args
}
