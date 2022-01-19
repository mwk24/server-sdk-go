package lksdk

import (
	"time"

	"github.com/livekit/protocol/livekit"
	"github.com/pion/webrtc/v3"
	"google.golang.org/protobuf/proto"
)

type LocalParticipant struct {
	baseParticipant
	Engine *RTCEngine
}

func newLocalParticipant(engine *RTCEngine, roomcallback *RoomCallback) *LocalParticipant {
	return &LocalParticipant{
		baseParticipant: *newBaseParticipant(roomcallback),
		Engine:          engine,
	}
}

func (p *LocalParticipant) PublishTrack(track webrtc.TrackLocal, name string) (*LocalTrackPublication, error) {
	kind := KindFromRTPType(track.Kind())
	pub := LocalTrackPublication{
		trackPublicationBase: trackPublicationBase{
			kind:   kind,
			track:  track,
			name:   name,
			client: p.Engine.client,
		},
	}
	err := p.Engine.client.SendRequest(&livekit.SignalRequest{
		Message: &livekit.SignalRequest_AddTrack{
			AddTrack: &livekit.AddTrackRequest{
				Cid:  track.ID(),
				Name: name,
				Type: kind.ProtoType(),
			},
		},
	})
	if err != nil {
		return nil, err
	}

	pubChan := p.Engine.TrackPublishedChan()
	var pubRes *livekit.TrackPublishedResponse

	select {
	case pubRes = <-pubChan:
		break
	case <-time.After(5 * time.Second):
		return nil, ErrTrackPublishTimeout
	}

	// add transceivers
	pub.transceiver, err = p.Engine.publisher.PeerConnection().AddTransceiverFromTrack(track, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionSendonly,
	})
	if err != nil {
		return nil, err
	}

	// read incoming rtcp packets so interceptors can handle NACKs
	go func() {
		sender := pub.transceiver.Sender()
		rtcpBuf := make([]byte, 1500)
		for {
			if _, _, rtcpErr := sender.Read(rtcpBuf); rtcpErr != nil {
				// pipe closed
				return
			}
		}
	}()

	pub.sid = pubRes.Track.Sid
	p.addPublication(&pub)

	p.Engine.publisher.Negotiate()

	logger.Info("published track", "track", name)

	return &pub, nil
}

func (p *LocalParticipant) PublishData(data []byte, kind livekit.DataPacket_Kind, destinationSids []string) error {
	packet := &livekit.DataPacket{
		Kind: kind,
		Value: &livekit.DataPacket_User{
			User: &livekit.UserPacket{
				// this is enforced on the server side, setting for completeness
				ParticipantSid:  p.sid,
				Payload:         data,
				DestinationSids: destinationSids,
			},
		},
	}

	if err := p.Engine.ensurePublisherConnected(); err != nil {
		return err
	}

	// encode packet
	encoded, err := proto.Marshal(packet)
	if err != nil {
		return err
	}

	if kind == livekit.DataPacket_RELIABLE {
		return p.Engine.reliableDC.Send(encoded)
	} else if kind == livekit.DataPacket_LOSSY {
		return p.Engine.lossyDC.Send(encoded)
	}

	return nil
}

func (p *LocalParticipant) UnpublishTrack(sid string) error {
	obj, loaded := p.tracks.LoadAndDelete(sid)
	if !loaded {
		return nil
	}
	p.audioTracks.Delete(sid)
	p.videoTracks.Delete(sid)

	pub, ok := obj.(*LocalTrackPublication)
	if !ok {
		return nil
	}

	var err error
	if localTrack, ok := pub.track.(webrtc.TrackLocal); ok {
		for _, sender := range p.Engine.publisher.pc.GetSenders() {
			if sender.Track() == localTrack {
				err = p.Engine.publisher.pc.RemoveTrack(sender)
				break
			}
		}
		p.Engine.publisher.Negotiate()
	}

	return err
}

func (p *LocalParticipant) updateInfo(info *livekit.ParticipantInfo) {
	p.baseParticipant.updateInfo(info, p)
	p.lock.Lock()
	p.sid = info.Sid
	p.identity = info.Identity
	p.lock.Unlock()

	// detect tracks that have been muted remotely, and apply changes
	for _, ti := range info.Tracks {
		pub := p.getLocalPublication(ti.Sid)
		if pub == nil {
			continue
		}
		if pub.IsMuted() != ti.Muted {
			pub.SetMuted(ti.Muted)

			// trigger callback
			if ti.Muted {
				p.Callback.OnTrackMuted(pub, p)
				p.roomCallback.OnTrackMuted(pub, p)
			} else if !ti.Muted {
				p.Callback.OnTrackUnmuted(pub, p)
				p.roomCallback.OnTrackUnmuted(pub, p)
			}
		}
	}
}

func (p *LocalParticipant) getLocalPublication(sid string) *LocalTrackPublication {
	if pub, ok := p.getPublication(sid).(*LocalTrackPublication); ok {
		return pub
	}
	return nil
}
