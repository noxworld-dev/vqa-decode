package movies

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"

	"github.com/youpy/go-wav"
)

type VqaHeader struct {
	Id            [8]byte
	StartPos      uint32
	Version       uint16
	VideoFlags    uint16
	FramesCount   uint16
	SizeX         uint16
	SizeY         uint16
	BlockSizeX    byte
	BlockSizeY    byte
	Fps           uint16
	ColorsCount   uint16
	MaxChunkSize  uint16
	Unk1          uint32
	Unk2          uint16
	SampleRate    uint16
	ChannelsCount byte
	BitsPerSample byte
	Unk3          [14]byte
}

type VqaChunkHeader struct {
	Id   [4]byte
	Size uint32
}

const vqaFormId = "FORM"
const vqaFileId = "WVQAVQHD"
const vqaSnd2Id = "SND2"
const vqaVqfrId = "VQFR"
const vqaVqflId = "VQFL"

type VqaFile struct {
	Header       VqaHeader
	CurrentChunk VqaChunkHeader

	handle    io.ReadSeeker
	lastError error

	dec *VqaDecoder
}

func OpenMovieWithHandle(handle io.ReadSeeker) (*VqaFile, error) {
	var vqa *VqaFile = new(VqaFile)
	vqa.handle = handle
	var err error = nil
	err = vqa.readChunkHeader()
	if err != nil || string(vqa.CurrentChunk.Id[:]) != vqaFormId {
		return nil, vqa.stickError(errors.New("VQA file unsupported"))
	}
	err = binary.Read(vqa.handle, binary.LittleEndian, &vqa.Header)
	if err != nil || string(vqa.Header.Id[:]) != vqaFileId {
		return nil, vqa.stickError(errors.New("VQA file unsupported"))
	}

	vqa.initDecoder()

	// Read the next chunk header too
	err = vqa.readChunkHeader()
	if err != nil {
		return nil, vqa.stickError(err)
	}

	return vqa, nil
}

func OpenMovie(filename string) (*VqaFile, io.Closer, error) {
	fileHandle, err := os.Open(filename)
	if err != nil {
		return nil, nil, err
	}
	res, err := OpenMovieWithHandle(fileHandle)
	return res, fileHandle, err
}

func (vqa *VqaFile) stickError(err error) error {
	vqa.lastError = err
	return vqa.lastError
}

func (vqa *VqaFile) readChunkHeader() error {
	curPos, err := vqa.handle.Seek(0, io.SeekCurrent)
	if err != nil {
		return vqa.stickError(err)
	}
	if curPos&1 == 1 {
		_, err = vqa.handle.Seek(1, io.SeekCurrent)
		if err != nil {
			return vqa.stickError(err)
		}
	}

	err = binary.Read(vqa.handle, binary.BigEndian, &vqa.CurrentChunk)
	if err != nil {
		return vqa.stickError(err)
	}
	return nil
}

func (vqa *VqaFile) skipChunk() error {
	_, err := vqa.handle.Seek(int64(vqa.CurrentChunk.Size), io.SeekCurrent)
	if err != nil {
		return vqa.stickError(err)
	}
	err = vqa.readChunkHeader()
	if err != nil {
		return vqa.stickError(err)
	}
	return nil
}

func (vqa *VqaFile) DumpAudio() error {
	if vqa.lastError != nil {
		return vqa.lastError
	}
	var filename = "./videoname.vqa"
	fileHandle, ok := vqa.handle.(*os.File)
	if ok {
		filename = fileHandle.Name()
	}
	var filepart = filename[:len(filename)-3]
	var soundfile = filepart + "wav"
	println(soundfile)
	var samples [][2]int16
	for {
		if string(vqa.CurrentChunk.Id[:]) == vqaSnd2Id {
			var data = make([]byte, vqa.CurrentChunk.Size)
			_, err := vqa.handle.Read(data)
			if err != nil {
				break
			}
			var decodedSamples = vqa.decodeSnd2Chunk(data)
			samples = append(samples, decodedSamples...)
			err = vqa.readChunkHeader()
			if err != nil {
				break
			}
		} else {
			err := vqa.skipChunk()
			if err != nil {
				break
			}
		}
	}
	out, _ := os.Create(soundfile)
	var writer = wav.NewWriter(out, uint32(len(samples)), uint16(vqa.Header.ChannelsCount), uint32(vqa.Header.SampleRate), uint16(vqa.Header.BitsPerSample))
	writer.WriteSamples(ConvertSamples(samples))
	out.Close()
	return nil
}

func (vqa *VqaFile) DumpVideo() error {
	var filename = "./videoname.vqa"
	fileHandle, ok := vqa.handle.(*os.File)
	if ok {
		filename = fileHandle.Name()
	}
	var foldername = filename[:len(filename)-4]
	os.Mkdir(foldername, os.ModeDir)
	var frameId = 0
	for {
		var filename = fmt.Sprintf("%s/%05d.png", foldername, frameId)
		if string(vqa.CurrentChunk.Id[:]) == vqaVqfrId || string(vqa.CurrentChunk.Id[:]) == vqaVqflId {
			var data = make([]byte, vqa.CurrentChunk.Size)
			_, err := vqa.handle.Read(data)
			if err != nil {
				break
			}
			var updated, frame = vqa.decodeVqfChunk(data)
			if updated {
				file, err := os.Create(filename)
				println(filename)
				if err != nil {
					return vqa.stickError(err)
				}
				err = png.Encode(file, &frame)
				if err != nil {
					return vqa.stickError(err)
				}
				file.Close()
				frameId++
			}
			vqa.readChunkHeader()
		} else {
			vqa.skipChunk()
		}
	}
	return nil
}

func (vqa *VqaFile) DecodeNextFrame() (frame *image.NRGBA, samples [][2]int16, err error) {
	for {
		if string(vqa.CurrentChunk.Id[:]) == vqaSnd2Id {
			var data = make([]byte, vqa.CurrentChunk.Size)
			_, err := vqa.handle.Read(data)
			if err != nil {
				return nil, samples, vqa.stickError(err)
			}
			var decodedSamples = vqa.decodeSnd2Chunk(data)
			samples = append(samples, decodedSamples...)
			err = vqa.readChunkHeader()
			if err != nil {
				return nil, samples, vqa.stickError(err)
			}
		} else if string(vqa.CurrentChunk.Id[:]) == vqaVqfrId || string(vqa.CurrentChunk.Id[:]) == vqaVqflId {
			var data = make([]byte, vqa.CurrentChunk.Size)
			_, err := vqa.handle.Read(data)
			if err != nil {
				return nil, samples, vqa.stickError(err)
			}
			updated, newFrame := vqa.decodeVqfChunk(data)
			vqa.readChunkHeader()
			if updated {

				return &newFrame, samples, nil
			}
		} else {
			err := vqa.skipChunk()
			if err != nil {
				return nil, samples, vqa.stickError(err)
			}
		}
	}
}

func ConvertSamples(samples [][2]int16) []wav.Sample {
	res := make([]wav.Sample, len(samples))
	for i := 0; i < len(samples); i++ {
		res[i] = wav.Sample{
			Values: [2]int{int(samples[i][0]), int(samples[i][1])},
		}
	}
	return res
}
