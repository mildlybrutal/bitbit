package bitbit

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

type torrentDataFile struct {
	Path   string
	Start  int64
	Length int64
}

func DownloadFromWebSeeds(torrent *TorrentFile, fw *FileWriter) error {
	if len(torrent.URLList) == 0 {
		return fmt.Errorf("torrent has no web seeds")
	}

	hashes := SplitPieceHashes(torrent.Info.Pieces)
	client := &http.Client{Timeout: 30 * time.Second}
	webSeeds := prioritizeWebSeeds(torrent.URLList)
	log.Printf("using web seed %s first", webSeeds[0])

	files := torrentDataFiles(torrent)
	for i, hash := range hashes {
		pieceLen := torrent.Info.PieceLength
		if i == len(hashes)-1 {
			pieceLen = torrent.Info.Length - (len(hashes)-1)*torrent.Info.PieceLength
		}

		start := int64(i) * int64(torrent.Info.PieceLength)
		end := start + int64(pieceLen) - 1

		data, sourceURL, err := fetchPieceFromWebSeeds(client, torrent, files, webSeeds, start, end, pieceLen)
		if err != nil {
			return fmt.Errorf("downloading piece %d from web seeds: %w", i, err)
		}
		if !VerifyPiece(data, hash) {
			return fmt.Errorf("piece %d hash mismatch from %s", i, sourceURL)
		}
		if err := fw.WritePiece(i, data); err != nil {
			return err
		}
		log.Printf("piece %d/%d done via web seed", i+1, len(hashes))
	}

	return fw.Close()
}

func fetchPieceFromWebSeeds(client *http.Client, torrent *TorrentFile, files []torrentDataFile, webSeeds []string, start, end int64, pieceLen int) ([]byte, string, error) {
	var errs []string

	for _, base := range webSeeds {
		data, sourceURL, err := fetchPieceFromWebSeed(client, torrent, files, base, start, end, pieceLen)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		return data, sourceURL, nil
	}

	return nil, "", errors.New(strings.Join(errs, "; "))
}

func fetchPieceFromWebSeed(client *http.Client, torrent *TorrentFile, files []torrentDataFile, base string, start, end int64, pieceLen int) ([]byte, string, error) {
	var buf bytes.Buffer
	var sourceURLs []string

	for _, tf := range files {
		fileEnd := tf.Start + tf.Length
		if end < tf.Start || start >= fileEnd {
			continue
		}

		rangeStart := maxInt64(start, tf.Start)
		rangeEnd := minInt64(end, fileEnd-1)
		localStart := rangeStart - tf.Start
		localEnd := rangeEnd - tf.Start

		fileURL, err := buildWebSeedFileURL(base, torrent, tf.Path)
		if err != nil {
			return nil, "", err
		}

		req, err := http.NewRequest(http.MethodGet, fileURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %v", fileURL, err)
		}
		req.Header.Set("Range", "bytes="+strconv.FormatInt(localStart, 10)+"-"+strconv.FormatInt(localEnd, 10))

		resp, err := client.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %v", fileURL, err)
		}
		if resp.StatusCode != http.StatusPartialContent {
			resp.Body.Close()
			return nil, "", fmt.Errorf("%s: unexpected status %s", fileURL, resp.Status)
		}

		chunkLen := rangeEnd - rangeStart + 1
		data, err := io.ReadAll(io.LimitReader(resp.Body, chunkLen+1))
		resp.Body.Close()
		if err != nil {
			return nil, "", fmt.Errorf("%s: %v", fileURL, err)
		}
		if int64(len(data)) != chunkLen {
			return nil, "", fmt.Errorf("%s: expected %d bytes, got %d", fileURL, chunkLen, len(data))
		}

		buf.Write(data)
		sourceURLs = append(sourceURLs, fileURL)
	}

	if buf.Len() != pieceLen {
		return nil, "", fmt.Errorf("%s: expected piece length %d, got %d", base, pieceLen, buf.Len())
	}

	return buf.Bytes(), strings.Join(sourceURLs, ", "), nil
}

func buildWebSeedFileURL(baseURL string, torrent *TorrentFile, filePath string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid web seed %q: %w", baseURL, err)
	}

	if torrent.Info.IsMultiFile() {
		fullPath := path.Join(strings.TrimSuffix(u.Path, "/"), torrent.Info.Name, filePath)
		if !strings.HasPrefix(fullPath, "/") {
			fullPath = "/" + fullPath
		}
		u.Path = fullPath
		u.RawPath = ""
		return u.String(), nil
	}

	if strings.HasSuffix(u.Path, "/") || u.Path == "" {
		fullPath := path.Join(strings.TrimSuffix(u.Path, "/"), torrent.Info.Name)
		if !strings.HasPrefix(fullPath, "/") {
			fullPath = "/" + fullPath
		}
		u.Path = fullPath
		u.RawPath = ""
	}

	return u.String(), nil
}

func torrentDataFiles(torrent *TorrentFile) []torrentDataFile {
	var files []torrentDataFile
	var offset int64

	if torrent.Info.IsMultiFile() {
		for _, tf := range torrent.Info.Files {
			files = append(files, torrentDataFile{
				Path:   path.Join(tf.Path...),
				Start:  offset,
				Length: int64(tf.Length),
			})
			offset += int64(tf.Length)
		}
		return files
	}

	files = append(files, torrentDataFile{
		Path:   torrent.Info.Name,
		Start:  0,
		Length: int64(torrent.Info.Length),
	})
	return files
}

func prioritizeWebSeeds(webSeeds []string) []string {
	prioritized := append([]string(nil), webSeeds...)
	sort.SliceStable(prioritized, func(i, j int) bool {
		return webSeedPriority(prioritized[i]) < webSeedPriority(prioritized[j])
	})
	return prioritized
}

func webSeedPriority(rawURL string) int {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 100
	}

	host := strings.ToLower(u.Hostname())
	switch {
	case host == "archive.archlinux.org":
		return 0
	case strings.Contains(host, "archlinux.org"):
		return 1
	case strings.Contains(host, "kernel.org"):
		return 2
	case strings.Contains(host, "leaseweb"):
		return 3
	case strings.Contains(host, "webtorrent.io"):
		return 4
	default:
		return 10
	}
}
