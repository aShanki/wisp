package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"wisp/daemon/internal/api"
)

func isMP4(mime, name string) bool {
	m := baseMime(mime)
	return m == "video/mp4" ||
		((m == "" || m == "application/octet-stream") && strings.EqualFold(filepath.Ext(name), ".mp4"))
}

// Probe the exact bytes that will be uploaded via a seekable file. MP4 may
// keep its metadata at the end and require seeking back to the first frame.
func prepareMP4(ctx context.Context, om *api.OutgoingMedia) error {
	f, err := os.CreateTemp("", "wisp-mp4-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(om.Data)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}

	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "ffprobe", "-v", "error", "-protocol_whitelist", "file",
		"-f", "mov", "-select_streams", "V:0", "-show_entries", "stream=width,height,duration:stream_side_data=rotation",
		"-of", "json", f.Name()).Output()
	if err != nil {
		return err
	}
	var probe struct {
		Streams []struct {
			Width        int
			Height       int
			Duration     string
			SideDataList []struct{ Rotation int } `json:"side_data_list"`
		}
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return err
	}
	if len(probe.Streams) == 0 || probe.Streams[0].Width <= 0 || probe.Streams[0].Height <= 0 {
		return errors.New("MP4 has no video stream")
	}
	stream := probe.Streams[0]
	om.Type, om.Mime = api.MsgVideo, "video/mp4"
	om.Width, om.Height = stream.Width, stream.Height
	for _, sd := range stream.SideDataList {
		if sd.Rotation%180 == 90 || sd.Rotation%180 == -90 {
			om.Width, om.Height = om.Height, om.Width
			break
		}
	}
	if seconds, err := strconv.ParseFloat(stream.Duration, 64); err == nil && seconds > 0 && seconds <= math.MaxUint32 {
		om.Duration = int(math.Ceil(seconds))
	}
	om.Thumb, _ = videoThumbnail(ctx, f.Name(), sendThumbPx)
	return nil
}

func videoThumbnail(ctx context.Context, path string, maxPx int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	scale := fmt.Sprintf("scale=%[1]d:%[1]d:force_original_aspect_ratio=decrease", maxPx)
	data, err := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-v", "error",
		"-protocol_whitelist", "file", "-f", "mov", "-threads", "1", "-i", path,
		"-map", "0:V:0", "-frames:v", "1", "-filter_threads", "1", "-vf", scale,
		"-c:v", "mjpeg", "-threads", "1", "-f", "image2pipe", "pipe:1").Output()
	if err == nil && len(data) == 0 {
		err = errors.New("MP4 has no decodable frame")
	}
	return data, err
}
