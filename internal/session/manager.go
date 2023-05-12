package session

import (
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/ooni/minivpn/internal/model"
	"github.com/ooni/minivpn/internal/optional"
)

// SessionNegotiationState is the state of the session negotiation.
type SessionNegotiationState int

const (
	// S_ERROR means there was some form of protocol error.
	S_ERROR = SessionNegotiationState(iota) - 1

	// S_UNDER is the undefined state.
	S_UNDEF

	// S_INITIAL means we're ready to begin the three-way handshake.
	S_INITIAL

	// S_PRE_START means we're waiting for acknowledgment from the remote.
	S_PRE_START

	// S_START means we've done the three-way handshake.
	S_START

	// S_SENT_KEY means we have sent the local part of the key_source2 random material.
	S_SENT_KEY

	// S_GOT_KEY means we have got the remote part of key_source2.
	S_GOT_KEY

	// S_ACTIVE means the control channel was established.
	S_ACTIVE

	// S_GENERATED_KEYS means the data channel keys have been generated.
	S_GENERATED_KEYS
)

// String maps a [SessionNegotiationState] to a string.
func (sns SessionNegotiationState) String() string {
	switch sns {
	case S_UNDEF:
		return "S_UNDEF"
	case S_INITIAL:
		return "S_INITIAL"
	case S_PRE_START:
		return "S_PRE_START"
	case S_START:
		return "S_START"
	case S_SENT_KEY:
		return "S_SENT_KEY"
	case S_GOT_KEY:
		return "S_GOT_KEY"
	case S_ACTIVE:
		return "S_ACTIVE"
	case S_GENERATED_KEYS:
		return "S_GENERATED_KEYS"
	case S_ERROR:
		return "S_ERROR"
	default:
		return "S_INVALID"
	}
}

// Manager manages the session. The zero value is invalid. Please, construct
// using [NewManager]. This struct is concurrency safe.
type Manager struct {
	keyID           uint8
	keys            []*DataChannelKey
	localSessionID  model.SessionID
	logger          model.Logger
	mu              sync.Mutex
	negState        SessionNegotiationState
	remoteSessionID optional.Value[model.SessionID]
}

// NewManager returns a [Manager] ready to be used.
func NewManager(logger model.Logger) (*Manager, error) {
	key0 := &DataChannelKey{}
	session := &Manager{
		keyID:           0,
		keys:            []*DataChannelKey{key0},
		localSessionID:  [8]byte{},
		logger:          logger,
		mu:              sync.Mutex{},
		negState:        0,
		remoteSessionID: optional.None[model.SessionID](),
	}

	randomBytes, err := randomFn(8)
	if err != nil {
		return session, err
	}

	// in go 1.17, one could do:
	//localSession := (*sessionID)(lsid)
	var localSession model.SessionID
	copy(localSession[:], randomBytes[:8])
	session.localSessionID = localSession

	localKey, err := NewKeySource()
	if err != nil {
		return session, err
	}

	k, err := session.ActiveKey()
	if err != nil {
		return session, err
	}
	k.local = localKey
	return session, nil
}

// LocalSessionID gets the local session ID.
func (m *Manager) LocalSessionID() model.SessionID {
	defer m.mu.Unlock()
	m.mu.Lock()
	return m.localSessionID
}

// IsRemoteSessionIDSet returns whether we've set the remote session ID.
func (m *Manager) IsRemoteSessionIDSet() bool {
	defer m.mu.Unlock()
	m.mu.Lock()
	return !m.remoteSessionID.IsNone()
}

// ErrNoRemoteSessionID indicates we are missing the remote session ID.
var ErrNoRemoteSessionID = errors.New("missing remote session ID")

// NewACKForPacket creates a new ACK for the given packet.
func (m *Manager) NewACKForPacket(packet *model.Packet) (*model.Packet, error) {
	defer m.mu.Unlock()
	m.mu.Lock()
	if m.remoteSessionID.IsNone() {
		return nil, ErrNoRemoteSessionID
	}
	p := &model.Packet{
		Opcode:          model.P_ACK_V1,
		KeyID:           m.keyID,
		PeerID:          [3]byte{},
		LocalSessionID:  m.localSessionID,
		ACKs:            []model.PacketID{packet.ID},
		RemoteSessionID: m.remoteSessionID.Unwrap(),
		ID:              0,
		Payload:         []byte{},
	}
	return p, nil
}

// NewPacket creates a new packet for this session.
func (m *Manager) NewPacket(opcode model.Opcode, payload []byte) *model.Packet {
	defer m.mu.Unlock()
	m.mu.Lock()
	return model.NewPacket(
		opcode,
		m.keyID,
		payload,
	)
}

// NegotiationState returns the state of the negotiation.
func (m *Manager) NegotiationState() SessionNegotiationState {
	defer m.mu.Unlock()
	m.mu.Lock()
	return m.negState
}

// SetNegotiationState sets the state of the negotiation.
func (m *Manager) SetNegotiationState(sns SessionNegotiationState) {
	defer m.mu.Unlock()
	m.mu.Lock()
	m.logger.Infof("[@] %s -> %s", m.negState, sns)
	m.negState = sns
}

// ActiveKey returns the dataChannelKey that is actively being used.
func (m *Manager) ActiveKey() (*DataChannelKey, error) {
	defer m.mu.Unlock()
	m.mu.Lock()
	if len(m.keys) > math.MaxUint8 || m.keyID >= uint8(len(m.keys)) {
		return nil, fmt.Errorf("%w: %s", errDataChannelKey, "no such key id")
	}
	dck := m.keys[m.keyID]
	// TODO(bassosimone): the following code would prevent us from
	// creating a new session at the beginning--refactor?
	/*
		if !dck.Ready() {
			return nil, fmt.Errorf("%w: %s", errDataChannelKey, "not ready")
		}
	*/
	return dck, nil
}
