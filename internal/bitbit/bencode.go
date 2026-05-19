package bitbit

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"strconv"
)

// Bencoding specification
// String: <length>:<string>
// Integers: i <int> e
// List: l <bencoded values> e
// Dictionary : d <bencoded string> <bencoded values> e

type TorrentFile struct {
	Announcer    string   `bencode:"announce"`
	Created_by   string   `bencode:"created_by"`
	Created_date string   `bencode:"created_date"`
	Encoding     string   `bencode:"encoding"`
	Comment      string   `bencode:"comment"`
	Info         InfoDict `bencode:"info"`
}

type InfoDict struct {
	PieceLength int    `bencode:"piece length"`
	Pieces      []byte `bencode:"pieces"`
	Name        string `bencode:"name"`

	Length int `bencode:"length"`

	Files []File `bencode:"files"`
}

type File struct {
	Length int      `bencode:"length"`
	Md5Sum string   `bencode:"md5Sum"`
	Path   []string `bencode:"path"`
}

func (i *InfoDict) IsMultiFile() bool {
	return len(i.Files) > 0
}

type Decoder struct {
	reader *bufio.Reader
}

func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{
		reader: bufio.NewReader(r),
	}
}

func (d *Decoder) Decode() (any, error) {
	ch, err := d.reader.Peek(1)
	if err != nil {
		return nil, err
	}

	switch ch[0] {
	case 'i':
		return d.decodeInt()
	case 'l':
		return d.decodeList()
	case 'd':
		return d.decodeDict()
	default:
		if ch[0] >= '0' && ch[0] <= '9' {
			return d.decodeString()
		}
		return nil, fmt.Errorf("invalid bencode type: %q", ch[0])
	}
}

func (d *Decoder) decodeString() (string, error) {
	lenBuf, err := d.reader.ReadString(':')
	if err != nil {
		return "", err
	}

	length, _ := strconv.Atoi(lenBuf[:len(lenBuf)-1])

	data := make([]byte, length)

	_, err = io.ReadFull(d.reader, data)

	return string(data), err
}

func (d *Decoder) decodeInt() (int64, error) {
	d.reader.ReadByte()
	data, err := d.reader.ReadString('e')
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(data[:len(data)-1], 10, 64)
}

func (d *Decoder) decodeList() ([]any, error) {
	d.reader.ReadByte()
	var list []any
	for {
		ch, err := d.reader.Peek(1)
		if err != nil {
			return nil, err
		}
		if ch[0] == 'e' {
			d.reader.ReadByte()
			break
		}
		val, err := d.Decode()
		if err != nil {
			return nil, err
		}
		list = append(list, val)
	}

	return list, nil
}

func (d *Decoder) decodeDict() (map[string]any, error) {
	d.reader.ReadByte() // Consumes 'd'
	dict := make(map[string]any)

	for {
		ch, err := d.reader.Peek(1)
		if err != nil {
			return nil, err
		}
		if ch[0] == 'e' {
			d.reader.ReadByte() // Consumes 'e'
			break
		}

		key, err := d.decodeString()
		if err != nil {
			return nil, err
		}
		val, err := d.Decode()
		if err != nil {
			return nil, err
		}
		dict[key] = val
	}
	return dict, nil
}

func MapToTorrent(raw map[string]any) (*TorrentFile, error) {
	t := &TorrentFile{}
	t.Announcer, _ = raw["announce"].(string)
	t.Created_by, _ = raw["created by"].(string)
	t.Created_date, _ = raw["creation date"].(string)
	t.Encoding, _ = raw["encoding"].(string)
	t.Comment, _ = raw["comment"].(string)

	infoRaw, ok := raw["info"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing or invalid info dict")
	}

	t.Info.Name, _ = infoRaw["name"].(string)

	if p, ok := infoRaw["pieces"].(string); ok {
		t.Info.Pieces = []byte(p)
	}
	if pl, ok := infoRaw["piece length"].(int64); ok {
		t.Info.PieceLength = int(pl)
	}
	if l, ok := infoRaw["length"].(int64); ok {
		t.Info.Length = int(l)
	}

	if filesRaw, ok := infoRaw["files"].([]any); ok {
		for _, f := range filesRaw {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			file := File{}
			if l, ok := fm["length"].(int64); ok {
				file.Length = int(l)
			}
			file.Md5Sum, _ = fm["md5sum"].(string)
			if pathRaw, ok := fm["path"].([]any); ok {
				for _, p := range pathRaw {
					if s, ok := p.(string); ok {
						file.Path = append(file.Path, s)
					}
				}
			}
			t.Info.Files = append(t.Info.Files, file)
		}
	}

	return t, nil
}

func SplitPieceHashes(pieces []byte) [][]byte {
	var hashes [][]byte
	for i := 0; i < len(pieces); i += 20 {
		hashes = append(hashes, pieces[i:i+20])
	}
	return hashes
}

func ExtractInfoDictBytes(data []byte) ([]byte, error) {
	start := []byte("4:info")
	idx := bytes.Index(data, start)
	if idx == -1 {
		return nil, fmt.Errorf("info dict not found")
	}

	i := idx + len(start)

	if data[i] != 'd' {
		return nil, fmt.Errorf("info dict not starting with d")
	}

	depth := 0
	for j := i; j < len(data); j++ {
		switch data[j] {
		case 'd', 'l':
			depth++
		case 'e':
			depth--
			if depth == 0 {
				return data[i : j+1], nil
			}
		}
	}
	return nil, fmt.Errorf("info dict end not found")
}

func ComputeInfoHash(raw []byte) ([]byte, error) {
	infoBytes, err := ExtractInfoDictBytes(raw)
	if err != nil {
		return nil, err
	}

	hash := sha1.Sum(infoBytes)
	return hash[:], nil
}

func ParseTorrentFile(path string) (*TorrentFile, []byte, error) {
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}

	infoHash, err := ComputeInfoHash(rawBytes)
	if err != nil {
		return nil, nil, err
	}

	decoded, err := NewDecoder(bytes.NewReader(rawBytes)).Decode()
	if err != nil {
		return nil, nil, err
	}

	dict := decoded.(map[string]any)
	torrent, err := MapToTorrent(dict)

	return torrent, infoHash, err
}
