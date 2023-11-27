package bgcodego

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"slices"
	"strings"

	heatshrink "github.com/currantlabs/goheatshrink"
)

// FileHeaderVersion for FileHeader
// Refer to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#file-header
type FileHeaderVersion uint32

func (fhv FileHeaderVersion) IsValid() bool {
	return fhv == Version1
}

const (
	Version1 FileHeaderVersion = 1
)

// ChecksumType for FileHeader
// Refer to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#file-header
type ChecksumType uint16

func (ct ChecksumType) IsValid() bool {
	return ct == ChecksumTypeNone || ct == ChecksumTypeCRC32
}

const (
	ChecksumTypeNone  ChecksumType = 0
	ChecksumTypeCRC32 ChecksumType = 1
)

// FileHeader implements https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#file-header
type FileHeader struct {
	MagicNumber  uint32
	Version      FileHeaderVersion // Version of the G-code binarization
	ChecksumType ChecksumType      // Algorithm used for checksum
}

func (fh *FileHeader) Parse(r io.Reader) error {
	if err := binary.Read(r, binary.LittleEndian, fh); err != nil {
		return err
	}
	if fh.MagicNumber != 1162101575 {
		return errors.New("invalid BGCode file")
	}
	if !fh.Version.IsValid() {
		return fmt.Errorf("non-supported bgcode version: %v", fh.Version)
	}
	if !fh.ChecksumType.IsValid() {
		return fmt.Errorf("non-supported checksum type: %v", fh.ChecksumType)
	}
	return nil
}

// BlockHeaderCompression according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#block-header
type BlockHeaderType uint16

func (bht BlockHeaderType) IsValid() bool {
	return bht == BlockHeaderTypeFileMetadata ||
		bht == BlockHeaderTypeGCode ||
		bht == BlockHeaderTypeSlicerMetadata ||
		bht == BlockHeaderTypePrinterMetadata ||
		bht == BlockHeaderTypePrintMetadata ||
		bht == BlockHeaderTypeThumbnail

}

const (
	BlockHeaderTypeFileMetadata    BlockHeaderType = 0
	BlockHeaderTypeGCode           BlockHeaderType = 1
	BlockHeaderTypeSlicerMetadata  BlockHeaderType = 2
	BlockHeaderTypePrinterMetadata BlockHeaderType = 3
	BlockHeaderTypePrintMetadata   BlockHeaderType = 4
	BlockHeaderTypeThumbnail       BlockHeaderType = 5
)

// BlockHeaderCompression according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#block-header
type BlockHeaderCompression uint16

func (bhc BlockHeaderCompression) String() string {
	switch bhc {
	case BlockHeaderCompressionNone:
		return "None"
	case BlockHeaderCompressionDeflate:
		return "Deflate"
	case BlockHeaderCompressionHeatshrink114:
		return "Heatshrink114"
	case BlockHeaderCompressionHeatshrink124:
		return "Heatshrink124"
	default:
		return "Unknown"
	}

}

func (bhc BlockHeaderCompression) IsValid() bool {
	return bhc == BlockHeaderCompressionNone ||
		bhc == BlockHeaderCompressionDeflate ||
		bhc == BlockHeaderCompressionHeatshrink114 ||
		bhc == BlockHeaderCompressionHeatshrink124
}

const (
	BlockHeaderCompressionNone          BlockHeaderCompression = 0
	BlockHeaderCompressionDeflate       BlockHeaderCompression = 1
	BlockHeaderCompressionHeatshrink114 BlockHeaderCompression = 2
	BlockHeaderCompressionHeatshrink124 BlockHeaderCompression = 3
)

// BlockHeader according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#block-header
type BlockHeader struct {
	basic struct {
		Type             BlockHeaderType
		Compression      BlockHeaderCompression
		UncompressedSize uint32
	}
	extended struct {
		CompressedSize uint32
	}
}

func (bh *BlockHeader) Type() BlockHeaderType {
	return bh.basic.Type
}

func (bh *BlockHeader) Parse(r io.Reader) error {
	if err := binary.Read(r, binary.LittleEndian, &bh.basic); err != nil {
		return err
	}
	if !bh.basic.Type.IsValid() {
		return fmt.Errorf("non-supported header type: %v", bh.basic.Type)
	}
	if !bh.basic.Compression.IsValid() {
		return fmt.Errorf("non-supported compression algorithm: %v", bh.basic.Compression)
	}
	if bh.basic.Compression == BlockHeaderCompressionNone {
		return nil
	}
	if err := binary.Read(r, binary.LittleEndian, &bh.extended); err != nil {
		return err
	}
	return nil
}

func (bh *BlockHeader) Length() uint32 {
	if bh.basic.Compression == BlockHeaderCompressionNone {
		return bh.basic.UncompressedSize
	}
	return bh.extended.CompressedSize
}

func (bh *BlockHeader) Compression() BlockHeaderCompression {
	return bh.basic.Compression
}

func (bh *BlockHeader) Inflate(body []byte) ([]byte, error) {
	switch bh.Compression() {
	case BlockHeaderCompressionDeflate:
		r, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("cannot create zlib inflator: %w", err)
		}
		return io.ReadAll(r)
	case BlockHeaderCompressionHeatshrink114:
		r := heatshrink.NewReader(bytes.NewReader(body), heatshrink.Window(11), heatshrink.Lookahead(4))
		return io.ReadAll(r)
	case BlockHeaderCompressionHeatshrink124:
		r := heatshrink.NewReader(bytes.NewReader(body), heatshrink.Window(12), heatshrink.Lookahead(4))
		return io.ReadAll(r)
	default:
		return body, nil
	}
}

type BlockEncoding uint16

const (
	BlockEncodingINI BlockEncoding = 0
)

// BlockFileMetadata according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#file-metadata
type BlockFileMetadata struct {
	header struct {
		Encoding BlockEncoding
	}
	Values KeyValues
}

func (bfm *BlockFileMetadata) Render() string {
	return "; generated by " + bfm.Values.First("Producer") + "\n\n"
}

func (bfm *BlockFileMetadata) Parse(r io.Reader, hdr *BlockHeader) error {
	if err := binary.Read(r, binary.LittleEndian, &bfm.header); err != nil {
		return err
	}
	switch bfm.header.Encoding {
	case BlockEncodingINI:
		body := make([]byte, hdr.Length())
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("cannot read block encoding: %w", err)
		}
		body, err := hdr.Inflate(body)
		if err != nil {
			return fmt.Errorf("cannot create body inflator: %w", err)
		}
		v, err := iniDecode(body)
		if err != nil {
			return fmt.Errorf("cannot decode INI key-table: %w", err)
		}
		bfm.Values = v
		return nil
	default:
		return errors.New("bad encoding")
	}
}

// BlockPrinterMetadata according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#printer-metadata
type BlockPrinterMetadata struct {
	header struct {
		Encoding BlockEncoding
	}
	Values KeyValues
}

func (bprm *BlockPrinterMetadata) Render() string {
	return bprm.Values.Render()
}

func (bprm *BlockPrinterMetadata) Parse(r io.Reader, hdr *BlockHeader) error {
	if err := binary.Read(r, binary.LittleEndian, &bprm.header); err != nil {
		return err
	}
	switch bprm.header.Encoding {
	case BlockEncodingINI:
		body := make([]byte, hdr.Length())
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("cannot read block encoding: %w", err)
		}
		body, err := hdr.Inflate(body)
		if err != nil {
			return fmt.Errorf("cannot create body inflator: %w", err)
		}
		v, err := iniDecode(body)
		if err != nil {
			return fmt.Errorf("cannot decode INI key-table: %w", err)
		}
		bprm.Values = v
		return nil
	default:
		return errors.New("bad encoding")
	}
}

// BlockThumbnailFormat according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#thumbnail
type BlockThumbnailFormat uint16

func (btf BlockThumbnailFormat) String() string {
	switch btf {
	case BlockThumbnailFormatPNG:
		return "PNG"
	case BlockThumbnailFormatJPG:
		return "JPG"
	case BlockThumbnailFormatQOI:
		return "QOI"
	default:
		return "Unknown"
	}
}

const (
	BlockThumbnailFormatPNG BlockThumbnailFormat = 0
	BlockThumbnailFormatJPG BlockThumbnailFormat = 1
	BlockThumbnailFormatQOI BlockThumbnailFormat = 2
)

// BlockThumbnail according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#thumbnail
type BlockThumbnail struct {
	header struct {
		Format BlockThumbnailFormat
		Width  uint16
		Height uint16
	}
	Body []byte
}

func (bt *BlockThumbnail) Render() string {
	out := &strings.Builder{}
	fmt.Fprintln(out, ";")
	encoded := base64.StdEncoding.EncodeToString(bt.Body)
	fmt.Fprintf(out, "; thumbnail begin %vx%v %v\n", bt.header.Width, bt.header.Height, len(encoded))
	for len(encoded) > 78 {
		chunk, rest := encoded[:78], encoded[78:]
		fmt.Fprintln(out, ";", chunk)
		encoded = rest
	}
	fmt.Fprintln(out, ";", encoded)
	fmt.Fprintf(out, "; thumbnail end\n")
	fmt.Fprintln(out, ";")

	return out.String()
}

func (bt *BlockThumbnail) Parse(r io.Reader, hdr *BlockHeader) error {
	if err := binary.Read(r, binary.LittleEndian, &bt.header); err != nil {
		return err
	}
	bt.Body = make([]byte, hdr.Length())
	_, err := io.ReadFull(r, bt.Body)
	return err
}

// BlockPrintMetadata according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#print-metadata
type BlockPrintMetadata struct {
	header struct {
		Encoding BlockEncoding
	}
	Values KeyValues
}

func (bprm *BlockPrintMetadata) Render() string {
	return bprm.Values.Render()
}

func (bprm *BlockPrintMetadata) Parse(r io.Reader, hdr *BlockHeader) error {
	if err := binary.Read(r, binary.LittleEndian, &bprm.header); err != nil {
		return err
	}
	switch bprm.header.Encoding {
	case BlockEncodingINI:
		body := make([]byte, hdr.Length())
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("cannot read block encoding: %w", err)
		}
		body, err := hdr.Inflate(body)
		if err != nil {
			return fmt.Errorf("cannot create body inflator: %w", err)
		}
		v, err := iniDecode(body)
		if err != nil {
			return fmt.Errorf("cannot decode INI key-table: %w", err)
		}
		bprm.Values = v
		return nil
	default:
		return errors.New("bad encoding")
	}
}

// BlockSlicerMetadata according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#slicer-metadata
type BlockSlicerMetadata struct {
	header struct {
		Encoding BlockEncoding
	}
	Values KeyValues
}

func (bsm *BlockSlicerMetadata) Render() string {
	out := &strings.Builder{}
	fmt.Fprintln(out, "; prusaslicer_config = begin")
	fmt.Fprint(out, bsm.Values.Render())
	fmt.Fprintln(out, "; prusaslicer_config = end")
	fmt.Fprintln(out, "")
	return out.String()
}

func (bsm *BlockSlicerMetadata) Parse(r io.Reader, hdr *BlockHeader) error {
	if err := binary.Read(r, binary.LittleEndian, &bsm.header); err != nil {
		return err
	}
	switch bsm.header.Encoding {
	case BlockEncodingINI:
		body := make([]byte, hdr.Length())
		if _, err := io.ReadFull(r, body); err != nil {
			return fmt.Errorf("cannot read block encoding: %w", err)
		}
		body, err := hdr.Inflate(body)
		if err != nil {
			return fmt.Errorf("cannot create body inflator: %w", err)
		}
		v, err := iniDecode(body)
		if err != nil {
			return fmt.Errorf("cannot decode INI key-table: %w", err)
		}
		bsm.Values = v
		return nil
	default:
		return errors.New("bad encoding")
	}
}

// GCodeEncoding according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#gcode
type GCodeEncoding uint16

const (
	GCodeEncodingNone                 GCodeEncoding = 0
	GCodeEncodingMeatpack             GCodeEncoding = 1
	GCodeEncodingMeatpackWithComments GCodeEncoding = 2
)

// BlockGCode according to https://github.com/prusa3d/libbgcode/blob/main/doc/specifications.md#gcode
type BlockGCode struct {
	header struct {
		Encoding GCodeEncoding
	}
	Body string
}

func (bg *BlockGCode) Render() string {
	return bg.Body
}

func (bg *BlockGCode) Parse(r io.Reader, hdr *BlockHeader) error {
	if err := binary.Read(r, binary.LittleEndian, &bg.header); err != nil {
		return err
	}
	body := make([]byte, hdr.Length())
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	body, err := hdr.Inflate(body)
	if err != nil {
		return err
	}
	bg.Body = unbinarize(body)
	return nil
}

type KeyValues []KeyValue

func (kv KeyValues) First(key string) string {
	idx := slices.IndexFunc(kv, func(kv KeyValue) bool {
		return kv.Key == key
	})
	if idx == -1 {
		return ""
	}
	return kv[idx].Value
}

func (kvs KeyValues) Render() string {
	out := &strings.Builder{}
	for _, kv := range kvs {
		line := fmt.Sprint("; ", kv.Key, " = ", kv.Value)
		fmt.Fprintln(out, strings.TrimSpace(line))
	}
	return out.String()
}

// KeyValue is the tuple used by tables inside of the blocks.
type KeyValue struct {
	Key   string
	Value string
}

func iniDecode(body []byte) (KeyValues, error) {
	var res KeyValues
	scanner := bufio.NewScanner(bytes.NewReader(body))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok {
			return nil, errors.New("malformed key-value pair")
		}
		res = append(res, KeyValue{
			Key:   strings.TrimSpace(key),
			Value: strings.TrimSpace(value),
		})
	}
	return res, nil
}

type BlockRenderer interface{ Render() string }

// Parse converts a BGCode input into regular GCode output
func Parse(fd io.Reader) (string, error) {
	out := &strings.Builder{}
	fh := &FileHeader{}
	if err := fh.Parse(fd); err != nil {
		return "", fmt.Errorf("cannot parse file header: %w", err)
	}
	blocks := make(map[BlockHeaderType][]BlockRenderer)
	for {
		buf := &bytes.Buffer{}
		r := io.TeeReader(fd, buf)
		hdr := &BlockHeader{}
		err := hdr.Parse(r)
		if errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", fmt.Errorf("cannot parse block header: %w", err)
		}

		var block interface {
			Parse(r io.Reader, hdr *BlockHeader) error
		}
		switch hdr.Type() {
		case BlockHeaderTypeFileMetadata:
			block = &BlockFileMetadata{}
		case BlockHeaderTypeGCode:
			block = &BlockGCode{}
		case BlockHeaderTypeSlicerMetadata:
			block = &BlockSlicerMetadata{}
		case BlockHeaderTypePrinterMetadata:
			block = &BlockPrinterMetadata{}
		case BlockHeaderTypePrintMetadata:
			block = &BlockPrintMetadata{}
		case BlockHeaderTypeThumbnail:
			block = &BlockThumbnail{}
		}
		if err := block.Parse(r, hdr); err != nil {
			return "", fmt.Errorf("cannot parse %q block: %w", hdr.Type(), err)
		}
		if fh.ChecksumType == ChecksumTypeCRC32 {
			var crc32footer uint32
			err := binary.Read(fd, binary.LittleEndian, &crc32footer)
			if err != nil {
				return "", fmt.Errorf("cannot read CRC32 footer: %w", err)
			}
			if crc32footer != crc32.ChecksumIEEE(buf.Bytes()) {
				return "", errors.New("bad checksum")
			}
		}
		blocks[hdr.Type()] = append(blocks[hdr.Type()], block.(BlockRenderer))
	}

	if b, ok := blocks[BlockHeaderTypeFileMetadata]; ok {
		fmt.Fprint(out, b[0].Render())
	}
	if b, ok := blocks[BlockHeaderTypePrinterMetadata]; ok {
		fmt.Fprintln(out)
		fmt.Fprint(out, b[0].Render())
	}
	if thumbnails, ok := blocks[BlockHeaderTypeThumbnail]; ok {
		for _, thumbnail := range thumbnails {
			fmt.Fprintln(out)
			fmt.Fprint(out, thumbnail.Render())
		}
	}
	if gcodes, ok := blocks[BlockHeaderTypeGCode]; ok {
		fmt.Fprintln(out)
		for _, gcode := range gcodes {
			fmt.Fprint(out, gcode.Render())
		}
	}
	if b, ok := blocks[BlockHeaderTypePrintMetadata]; ok {
		fmt.Fprintln(out)
		fmt.Fprint(out, b[0].Render())
	}
	if b, ok := blocks[BlockHeaderTypeSlicerMetadata]; ok {
		fmt.Fprintln(out)
		fmt.Fprint(out, b[0].Render())
	}
	return out.String(), nil
}
