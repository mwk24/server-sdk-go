package lksdk

import (
	"encoding/binary"
	"errors"
	"os"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"github.com/pion/webrtc/v3/pkg/media/ivfreader"

	"github.com/livekit/livekit-sdk-go/media/h264reader"
)

type SampleProvider interface {
	NextSample() (media.Sample, error)
}

// NullSampleProvider is a media provider that provides null packets, it could meet a certain bitrate, if desired
type NullSampleProvider struct {
	BytesPerSample uint32
	SampleDuration time.Duration
}

func NewNullSampleProvider(bitrate uint32) *NullSampleProvider {
	return &NullSampleProvider{
		SampleDuration: time.Second / 30,
		BytesPerSample: bitrate / 8 / 30,
	}
}

func (p *NullSampleProvider) NextSample() (media.Sample, error) {
	return media.Sample{
		Data:     make([]byte, p.BytesPerSample),
		Duration: p.SampleDuration,
	}, nil
}

type LoadTestProvider struct {
	BytesPerSample uint32
	SampleDuration time.Duration
}

func NewLoadTestProvider(bitrate uint32) (*LoadTestProvider, error) {
	bps := bitrate / 8 / 30
	if bps < 8 {
		return nil, errors.New("bitrate lower than minimum of 1920")
	}

	return &LoadTestProvider{
		SampleDuration: time.Second / 30,
		BytesPerSample: bps,
	}, nil
}

func (p *LoadTestProvider) NextSample() (media.Sample, error) {
	ts := make([]byte, 8)
	binary.LittleEndian.PutUint64(ts, uint64(time.Now().UnixNano()))
	packet := append(make([]byte, p.BytesPerSample-8), ts...)

	return media.Sample{
		Data:     packet,
		Duration: p.SampleDuration,
	}, nil
}

var c int

func NewFileSampleProvider(f *os.File, mimeType string) (SampleProvider, webrtc.RTPCodecCapability, error) {
	switch mimeType {
	case webrtc.MimeTypeOpus:
		return nil, webrtc.RTPCodecCapability{}, errors.New("coming soon")
	case webrtc.MimeTypeH264:
		reader, err := h264reader.NewReader(f)
		if err != nil {
			return nil, webrtc.RTPCodecCapability{}, err
		}
		provider := &H264VideoProvider{
			reader: reader,
		}
		codec := webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeH264,
			ClockRate: 90000,
			RTCPFeedback: []webrtc.RTCPFeedback{
				{"goog-remb", ""},
				{"ccm", "fir"},
				{"nack", ""},
				{"nack", "pli"},
			},
		}

		switch c % 6 {
		case 0:
			codec.SDPFmtpLine = "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42001f"
		case 1:
			codec.SDPFmtpLine = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42001f"
		case 2:
			codec.SDPFmtpLine = "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=42e01f"
		case 3:
			codec.SDPFmtpLine = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"
		case 4:
			codec.SDPFmtpLine = "level-asymmetry-allowed=1;packetization-mode=0;profile-level-id=640032"
		case 5:
			codec.SDPFmtpLine = "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=640032"
		}
		c++

		return provider, codec, nil
	case webrtc.MimeTypeVP8:
		reader, header, err := ivfreader.NewWith(f)
		if err != nil {
			return nil, webrtc.RTPCodecCapability{}, err
		}

		provider := &VP8VideoProvider{
			reader:         reader,
			sampleDuration: time.Millisecond * time.Duration((float32(header.TimebaseNumerator)/float32(header.TimebaseDenominator))*1000),
		}
		codec := webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeVP8,
		}

		return provider, codec, nil
	default:
		return nil, webrtc.RTPCodecCapability{}, errors.New("format not supported")
	}
}

type OpusAudioProvider struct{}

func (p *OpusAudioProvider) NextSample() (media.Sample, error) {
	return media.Sample{}, nil
}

type H264VideoProvider struct {
	reader *h264reader.H264Reader
}

func (p *H264VideoProvider) NextSample() (media.Sample, error) {
	nal, err := p.reader.NextNAL()
	if err != nil {
		return media.Sample{}, err
	}
	return media.Sample{
		Data:     nal.Data,
		Duration: time.Second / 30,
	}, nil
}

type VP8VideoProvider struct {
	reader         *ivfreader.IVFReader
	sampleDuration time.Duration
}

func (p *VP8VideoProvider) NextSample() (media.Sample, error) {
	frame, _, err := p.reader.ParseNextFrame()
	if err != nil {
		return media.Sample{}, err
	}

	return media.Sample{
		Data:     frame,
		Duration: p.sampleDuration,
	}, nil
}
