package moqtransport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"time"

	"gitlab.lrz.de/cm/moqtransport/varint"
	"golang.org/x/exp/slices"
)

var (
	errUnexpectedMessage     = errors.New("got unexpected message")
	errInvalidTrackNamespace = errors.New("got invalid tracknamespace")
	errClosed                = errors.New("connection was closed")
)

// TODO: Streams must be wrapped properly for quic and webtransport The
// interfaces need to add CancelRead and CancelWrite for STOP_SENDING and
// RESET_STREAM purposes. The interface should allow implementations for quic
// and webtransport.
type stream interface {
	readStream
	sendStream
}

type readStream interface {
	io.Reader
}

type sendStream interface {
	io.WriteCloser
}

type connection interface {
	OpenStream() (stream, error)
	OpenStreamSync(context.Context) (stream, error)
	OpenUniStream() (sendStream, error)
	OpenUniStreamSync(context.Context) (sendStream, error)
	AcceptStream(context.Context) (stream, error)
	AcceptUniStream(context.Context) (readStream, error)
	ReceiveMessage(context.Context) ([]byte, error)
}

type SubscriptionHandler func(string, *SendTrack) (uint64, time.Duration, error)

type AnnouncementHandler func(string) error

type Peer struct {
	conn                connection
	inMsgCh             chan message
	ctrlMessageCh       chan message
	ctrlStream          stream
	role                role
	receiveTracks       map[uint64]*ReceiveTrack
	sendTracks          map[string]*SendTrack
	subscribeHandler    SubscriptionHandler
	announcementHandler AnnouncementHandler
	closeCh             chan struct{}
}

func newServerPeer(ctx context.Context, conn connection) (*Peer, error) {
	s, err := conn.AcceptStream(ctx)
	if err != nil {
		return nil, err
	}
	m, err := readNext(varint.NewReader(s), serverRole)
	if err != nil {
		return nil, err
	}
	msg, ok := m.(*clientSetupMessage)
	if !ok {
		return nil, errUnexpectedMessage
	}
	// TODO: Algorithm to select best matching version
	if !slices.Contains(msg.SupportedVersions, DRAFT_IETF_MOQ_TRANSPORT_00) {
		// TODO: Close conn with error
		log.Println("TODO: Close conn with error")
		return nil, nil
	}
	_, ok = msg.SetupParameters[roleParameterKey]
	if !ok {
		// ERROR: role is required
		log.Println("TODO: ERROR: role is required")
		return nil, nil
	}
	// TODO: save role parameter
	ssm := serverSetupMessage{
		SelectedVersion: DRAFT_IETF_MOQ_TRANSPORT_00,
		SetupParameters: map[parameterKey]parameter{},
	}
	buf := ssm.append(make([]byte, 0, 1500))
	_, err = s.Write(buf)
	if err != nil {
		return nil, err
	}
	p := &Peer{
		conn:                conn,
		inMsgCh:             make(chan message),
		ctrlMessageCh:       make(chan message),
		ctrlStream:          s,
		role:                serverRole,
		receiveTracks:       map[uint64]*ReceiveTrack{},
		sendTracks:          map[string]*SendTrack{},
		subscribeHandler:    nil,
		announcementHandler: nil,
		closeCh:             make(chan struct{}),
	}
	return p, nil
}

func (p *Peer) runServerPeer(ctx context.Context) {
	go p.controlStreamLoop(ctx, p.ctrlStream)
	go p.acceptBidirectionalStreams(ctx)
	go p.acceptUnidirectionalStreams(ctx)
	// TODO: Configure if datagrams enabled?
	go p.acceptDatagrams(ctx)
}

func newClientPeer(ctx context.Context, conn connection) (*Peer, error) {
	s, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, err
	}
	csm := clientSetupMessage{
		SupportedVersions: []version{version(DRAFT_IETF_MOQ_TRANSPORT_00)},
		SetupParameters: map[parameterKey]parameter{
			roleParameterKey: ingestionDeliveryRole,
		},
	}
	buf := csm.append(make([]byte, 0, 1500))
	_, err = s.Write(buf)
	if err != nil {
		return nil, err
	}
	p := &Peer{
		conn:                conn,
		inMsgCh:             make(chan message),
		ctrlMessageCh:       make(chan message),
		ctrlStream:          s,
		role:                clientRole,
		receiveTracks:       map[uint64]*ReceiveTrack{},
		sendTracks:          map[string]*SendTrack{},
		subscribeHandler:    nil,
		announcementHandler: nil,
		closeCh:             make(chan struct{}),
	}
	m, err := readNext(varint.NewReader(s), clientRole)
	if err != nil {
		return nil, err
	}
	_, ok := m.(*serverSetupMessage)
	if !ok {
		return nil, errUnexpectedMessage
	}

	go p.controlStreamLoop(ctx, s)
	go p.acceptBidirectionalStreams(ctx)
	go p.acceptUnidirectionalStreams(ctx)
	// TODO: Configure if datagrams enabled?
	go p.acceptDatagrams(ctx)
	return p, nil
}

func (p *Peer) readMessages(r messageReader, stream io.Reader) {
	for {
		msg, err := readNext(r, p.role)
		if err != nil {
			// TODO: Handle/log error?
			log.Println(err)
			return
		}
		object, ok := msg.(*objectMessage)
		if !ok {
			// TODO: ERROR: We only expect object messages here. All other
			// messages should be sent on the control stream.
			panic("unexpected message type")
		}
		p.handleObjectMessage(object)
	}
}

type messageKey struct {
	mt messageType
	id string
}

type keyer interface {
	key() messageKey
}

type keyedMessage interface {
	message
	keyer
}

type responseHandler interface {
	handle(message)
}

type keyedResponseHandler interface {
	keyedMessage
	responseHandler
}

func (p *Peer) controlStreamLoop(ctx context.Context, s stream) {
	inCh := make(chan message)
	errCh := make(chan error)
	transactions := make(map[messageKey]keyedMessage)

	go func(s stream, ch chan<- message, errCh chan<- error) {
		for {
			msg, err := readNext(varint.NewReader(s), p.role)
			if err != nil {
				errCh <- err
				return
			}
			ch <- msg
		}
	}(s, inCh, errCh)
	for {
		select {
		case m := <-inCh:
			log.Printf("handling %v\n", m)
			switch v := m.(type) {
			case *subscribeRequestMessage:
				go func() {
					p.ctrlMessageCh <- p.handleSubscribeRequest(v)
				}()
			case *announceMessage:
				go func() {
					p.ctrlMessageCh <- p.handleAnnounceMessage(v)
				}()
			case *goAwayMessage:
				panic("TODO")
			case keyedMessage:
				t, ok := transactions[v.key()]
				if !ok {
					// TODO: Error: This an error, because all keyed messages
					// that occur without responding to a transaction started by
					// us should be handled by the case above. I.e., if we get
					// an Ok or Error for Announce or subscribe, that should
					// only happen when we also stored an associated transaction
					// earlier.
					panic("TODO")
				}
				rh, ok := t.(responseHandler)
				if !ok {
					// TODO: Error: This is also an error, because we shouldn't
					// have started a transaction if we cannot handle the
					// response
					panic("TODO")
				}
				rh.handle(v)
			default:
				// error unexpected message, close conn?
				panic("TODO")
			}
		case m := <-p.ctrlMessageCh:
			if krh, ok := m.(keyedResponseHandler); ok {
				transactions[krh.key()] = krh
			}
			buf := make([]byte, 0, 1500)
			buf = m.append(buf)
			_, err := s.Write(buf)
			if err != nil {
				// TODO
				log.Println(err)
			}
		case err := <-errCh:
			// TODO
			log.Println(err)
			panic(err)
		}
	}
}

func (p *Peer) acceptBidirectionalStreams(ctx context.Context) {
	for {
		s, err := p.conn.AcceptStream(ctx)
		if err != nil {
			// TODO: Handle/log error?
			log.Println(err)
			return
		}
		go p.readMessages(varint.NewReader(s), s)
	}
}

func (p *Peer) acceptUnidirectionalStreams(ctx context.Context) {
	for {
		stream, err := p.conn.AcceptUniStream(ctx)
		if err != nil {
			// TODO: Handle/log error?
			log.Println(err)
			return
		}
		go p.readMessages(varint.NewReader(stream), stream)
	}
}

func (p *Peer) acceptDatagrams(ctx context.Context) {
	for {
		dgram, err := p.conn.ReceiveMessage(ctx)
		if err != nil {
			// TODO: Handle/log error?
			log.Println(err)
			return
		}
		r := bytes.NewReader(dgram)
		go p.readMessages(r, nil)
	}
}

func (p *Peer) handleObjectMessage(msg *objectMessage) error {
	t, ok := p.receiveTracks[msg.TrackID]
	if !ok {
		// handle unknown track?
		panic("TODO")
	}
	t.push(msg)
	return nil
}

func (p *Peer) handleSubscribeRequest(msg *subscribeRequestMessage) message {
	if p.subscribeHandler == nil {
		panic("TODO")
	}
	t := newSendTrack(p.conn)
	p.sendTracks[msg.FullTrackName] = t
	id, expires, err := p.subscribeHandler(msg.FullTrackName, t)
	if err != nil {
		log.Println(err)
		return &subscribeErrorMessage{
			FullTrackName: msg.FullTrackName,
			ErrorCode:     GenericErrorCode,
			ReasonPhrase:  "failed to handle subscription",
		}
	}
	t.id = id
	return &subscribeOkMessage{
		FullTrackName: msg.FullTrackName,
		TrackID:       id,
		Expires:       expires,
	}
}

func (p *Peer) handleAnnounceMessage(msg *announceMessage) message {
	if p.announcementHandler == nil {
		panic("TODO")
	}
	if err := p.announcementHandler(msg.TrackNamespace); err != nil {
		return &announceErrorMessage{
			TrackNamespace: msg.TrackNamespace,
			ErrorCode:      0,
			ReasonPhrase:   "failed to handle announcement",
		}
	}
	return &announceOkMessage{
		TrackNamespace: msg.TrackNamespace,
	}
}

type ctrlMessage struct {
	keyedMessage
	responseCh chan message
}

func (m *ctrlMessage) handle(msg message) {
	m.responseCh <- msg
}

func (p *Peer) Announce(namespace string) error {
	if len(namespace) == 0 {
		return errInvalidTrackNamespace
	}
	am := &announceMessage{
		TrackNamespace:  namespace,
		TrackParameters: map[parameterKey]parameter{},
	}
	responseCh := make(chan message)
	select {
	case p.ctrlMessageCh <- &ctrlMessage{
		keyedMessage: am,
		responseCh:   responseCh,
	}:
	case <-p.closeCh:
		return errClosed
	}
	var resp message
	select {
	case resp = <-responseCh:
	case <-time.After(time.Second): // TODO: Make timeout configurable?
		panic("TODO: timeout error")
	case <-p.closeCh:
		return errClosed
	}
	switch v := resp.(type) {
	case *announceOkMessage:
		if v.TrackNamespace != am.TrackNamespace {
			panic("TODO")
		}
	case *announceErrorMessage:
		return errors.New(v.ReasonPhrase) // TODO: Wrap error string?
	default:
		return errUnexpectedMessage
	}
	return nil
}

func (p *Peer) Subscribe(trackname string) (*ReceiveTrack, error) {
	sm := &subscribeRequestMessage{
		FullTrackName:          trackname,
		TrackRequestParameters: map[parameterKey]parameter{},
	}
	responseCh := make(chan message)
	select {
	case p.ctrlMessageCh <- &ctrlMessage{
		keyedMessage: sm,
		responseCh:   responseCh,
	}:
	case <-p.closeCh:
		return nil, errClosed
	case <-time.After(time.Second):
		panic("TODO: timeout error")
	}
	var resp message
	select {
	case resp = <-responseCh:
	case <-time.After(time.Second): // TODO: Make timeout configurable?
		panic("TODO: timeout error")
	case <-p.closeCh:
		return nil, errClosed
	}
	switch v := resp.(type) {
	case *subscribeOkMessage:
		if v.FullTrackName != sm.FullTrackName {
			panic("TODO")
		}
		t := newReceiveTrack()
		p.receiveTracks[v.TrackID] = t
		return t, nil

	case *subscribeErrorMessage:
		return nil, errors.New(v.ReasonPhrase)
	}
	return nil, errUnexpectedMessage
}

func (p *Peer) OnAnnouncement(callback AnnouncementHandler) {
	p.announcementHandler = callback
}

func (p *Peer) OnSubscription(callback SubscriptionHandler) {
	p.subscribeHandler = callback
}