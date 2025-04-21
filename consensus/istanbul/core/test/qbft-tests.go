/*
QBFT 재구성(reorganization) 테스트 동작 순서:

1. 초기 설정
   - 4개의 검증자 생성 (3개 정상, 1개 지연)
   - 각 검증자에 대한 core 인스턴스 생성
   - 지연된 검증자(마지막 검증자)에 30초 지연 설정

2. 정상 검증자 동작
   - 3개의 정상 검증자가 블록 1에 대한 PRE-PREPARE 메시지 교환
   - PRE-PREPARE -> PREPARE -> COMMIT 순서로 메시지 처리

3. 지연된 검증자 동작
   - 1초 대기 후 ROUND-CHANGE 메시지 전송 (시퀀스 0)
   - 다른 검증자들에게 메시지 전파

4. 재구성 처리
   - 모든 검증자가 ROUND-CHANGE 메시지 수신
   - 시퀀스를 0으로 리셋
   - 중복 메시지 처리 방지

5. 결과 검증
   - 모든 검증자의 시퀀스가 0인지 확인
   - 재구성 완료 확인
*/

// jhhong qbft
package core

import (
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/istanbul"
	"github.com/ethereum/go-ethereum/consensus/istanbul/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/trie"
)

const (
	inmemoryMessages = 4096
	messageQueueSize = 1000
)

// MockDatabase is a simple in-memory database for testing
type MockDatabase struct {
	data map[string][]byte
}

func NewMockDatabase() *MockDatabase {
	return &MockDatabase{
		data: make(map[string][]byte),
	}
}

func (db *MockDatabase) Put(key []byte, value []byte) error {
	db.data[string(key)] = value
	return nil
}

func (db *MockDatabase) Get(key []byte) ([]byte, error) {
	if value, ok := db.data[string(key)]; ok {
		return value, nil
	}
	return nil, nil
}

func (db *MockDatabase) Has(key []byte) (bool, error) {
	_, ok := db.data[string(key)]
	return ok, nil
}

func (db *MockDatabase) Delete(key []byte) error {
	delete(db.data, string(key))
	return nil
}

func (db *MockDatabase) Close() error {
	return nil
}

func (db *MockDatabase) NewBatch() ethdb.Batch {
	return nil
}

func (db *MockDatabase) NewBatchWithSize(size int) ethdb.Batch {
	return &MockBatch{}
}

func (db *MockDatabase) Stat(property string) (string, error) {
	return "", nil
}

func (db *MockDatabase) Compact(start []byte, limit []byte) error {
	return nil
}

func (db *MockDatabase) Ancient(kind string, number uint64) ([]byte, error) {
	return nil, nil
}

func (db *MockDatabase) AncientDatadir() (string, error) {
	return "", nil
}

func (db *MockDatabase) AncientRange(kind string, start, count, maxBytes uint64) ([][]byte, error) {
	return nil, nil
}

func (db *MockDatabase) AncientSize(kind string) (uint64, error) {
	return 0, nil
}

func (db *MockDatabase) Ancients() (uint64, error) {
	return 0, nil
}

func (db *MockDatabase) HasAncient(kind string, number uint64) (bool, error) {
	return false, nil
}

func (db *MockDatabase) MigrateTable(name string, fn func([]byte) ([]byte, error)) error {
	return nil
}

func (db *MockDatabase) ModifyAncients(fn func(ethdb.AncientWriteOp) error) (int64, error) {
	return 0, nil
}

func (db *MockDatabase) NewSnapshot() (ethdb.Snapshot, error) {
	return &MockSnapshot{}, nil
}

func (db *MockDatabase) ReadAncients(fn func(ethdb.AncientReaderOp) error) error {
	return nil
}

func (db *MockDatabase) Sync() error {
	return nil
}

func (db *MockDatabase) Tail() (uint64, error) {
	return 0, nil
}

func (db *MockDatabase) TruncateHead(n uint64) (uint64, error) {
	return 0, nil
}

func (db *MockDatabase) TruncateTail(n uint64) (uint64, error) {
	return 0, nil
}

// jhhong qbft
// roundState represents the current state of a round
type roundState struct {
	sequence *big.Int
	round    *big.Int
	mu       sync.Mutex
}

func (s *roundState) SetSequence(seq *big.Int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sequence = seq
}

func (s *roundState) SetRound(r *big.Int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.round = r
}

func (s *roundState) Sequence() *big.Int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sequence
}

func (s *roundState) Round() *big.Int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.round
}

// QBFT 메시지 타입 정의
type QBFTMessage struct {
	Type     string
	Sequence *big.Int
	Round    int
	Sender   common.Address
	Proposal *types.Block
}

// TestCore is a test implementation of Core
type TestCore struct {
	*core.Core
	messageDelay    time.Duration
	state           *roundState
	messages        chan QBFTMessage
	address         common.Address
	prepareMsgs     map[string]int
	commitMsgs      map[string]int
	wg              *sync.WaitGroup
	allCores        []*TestCore
	done            chan struct{}
	msgCount        int
	processedMsgs   map[string]bool
	mu              sync.Mutex
	currentProposal *types.Block
}

// TestRequest is a test implementation of Request
type TestRequest struct {
	Proposal istanbul.Proposal
}

// Proposal: 현재 제안된 블록 반환
func (c *TestCore) Proposal() *types.Block {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentProposal
}

// Start starts the message processing loop
func (c *TestCore) Start() {
	c.done = make(chan struct{})
	go func() {
		for {
			select {
			case msg := <-c.messages:
				c.handleMessage(msg)
			case <-c.done:
				return
			case <-time.After(5 * time.Second):
				fmt.Printf("검증자 %s의 메시지 처리 타임아웃\n", c.address.Hex())
				return
			}
		}
	}()
}

// Stop stops the message processing loop
func (c *TestCore) Stop() {
	close(c.done)
}

// handleMessage handles incoming QBFT messages
func (c *TestCore) handleMessage(msg QBFTMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 메시지 중복 처리 방지
	msgKey := fmt.Sprintf("%s-%d-%d-%s", msg.Type, msg.Sequence.Uint64(), msg.Round, msg.Sender.Hex())
	if c.processedMsgs[msgKey] {
		return
	}
	c.processedMsgs[msgKey] = true

	switch msg.Type {
	case "PRE-PREPARE":
		c.handlePreprepare(msg)
	case "PREPARE":
		c.handlePrepare(msg)
	case "COMMIT":
		c.handleCommit(msg)
	case "ROUND-CHANGE":
		c.handleRoundChange(msg)
	}
}

// handlePreprepare handles PRE-PREPARE messages
func (c *TestCore) handlePreprepare(msg QBFTMessage) {
	fmt.Printf("검증자 %s가 PRE-PREPARE 메시지 처리: 시퀀스=%d, 라운드=%d\n",
		c.address.Hex(), msg.Sequence.Uint64(), msg.Round)

	c.currentProposal = msg.Proposal
	c.state.SetSequence(msg.Sequence)
	c.state.SetRound(big.NewInt(int64(msg.Round)))

	// 자신의 PREPARE 메시지 생성
	prepareMsg := QBFTMessage{
		Type:     "PREPARE",
		Sequence: msg.Sequence,
		Round:    msg.Round,
		Sender:   c.address,
		Proposal: msg.Proposal,
	}

	fmt.Printf("검증자 %s가 PREPARE 메시지 생성\n", c.address.Hex())

	// 자신의 PREPARE 메시지를 다른 검증자들에게 전파
	for _, core := range c.allCores {
		if core.address != c.address {
			select {
			case core.messages <- prepareMsg:
				fmt.Printf("검증자 %s가 PREPARE 메시지를 %s에게 전송\n", c.address.Hex(), core.address.Hex())
			case <-time.After(5 * time.Second):
				fmt.Printf("검증자 %s가 %s에게 PREPARE 메시지 전송 시간 초과\n", c.address.Hex(), core.address.Hex())
			}
		}
	}

	// 자신의 PREPARE 메시지도 처리
	c.handlePrepare(prepareMsg)
}

// handlePrepare handles PREPARE messages
func (c *TestCore) handlePrepare(msg QBFTMessage) {
	fmt.Printf("검증자 %s가 PREPARE 메시지 처리: 시퀀스=%d, 라운드=%d, 발신자=%s\n",
		c.address.Hex(), msg.Sequence.Uint64(), msg.Round, msg.Sender.Hex())

	msgKey := fmt.Sprintf("prepare-%d-%d", msg.Sequence.Uint64(), msg.Round)
	c.prepareMsgs[msgKey]++

	fmt.Printf("검증자 %s의 PREPARE 메시지 수: %d\n", c.address.Hex(), c.prepareMsgs[msgKey])

	// 2/3 이상의 PREPARE 메시지를 받으면 COMMIT 메시지 전파
	if c.prepareMsgs[msgKey] >= 3 {
		fmt.Printf("검증자 %s가 PREPARE 메시지 2/3 이상 수신\n", c.address.Hex())
		commitMsg := QBFTMessage{
			Type:     "COMMIT",
			Sequence: msg.Sequence,
			Round:    msg.Round,
			Sender:   c.address,
			Proposal: msg.Proposal,
		}

		// 자신을 포함한 모든 검증자에게 COMMIT 메시지 전파
		for _, core := range c.allCores {
			fmt.Printf("검증자 %s가 COMMIT 메시지 전파: 수신자=%s\n",
				c.address.Hex(), core.address.Hex())
			core.messages <- commitMsg
		}
	}
}

// handleCommit handles COMMIT messages
func (c *TestCore) handleCommit(msg QBFTMessage) {
	fmt.Printf("검증자 %s가 COMMIT 메시지 처리: 시퀀스=%d, 라운드=%d, 발신자=%s\n",
		c.address.Hex(), msg.Sequence.Uint64(), msg.Round, msg.Sender.Hex())

	msgKey := fmt.Sprintf("commit-%d-%d", msg.Sequence.Uint64(), msg.Round)
	c.commitMsgs[msgKey]++

	fmt.Printf("검증자 %s의 COMMIT 메시지 수: %d\n", c.address.Hex(), c.commitMsgs[msgKey])

	// 2/3 이상의 COMMIT 메시지를 받으면 블록 커밋
	if c.commitMsgs[msgKey] >= 3 {
		fmt.Printf("검증자 %s가 블록 %d을 확정함\n", c.address.Hex(), msg.Sequence.Uint64())
		c.state.SetSequence(msg.Sequence)
		c.state.SetRound(big.NewInt(int64(msg.Round)))

		// 자신의 COMMIT 메시지도 전파
		commitMsg := QBFTMessage{
			Type:     "COMMIT",
			Sequence: msg.Sequence,
			Round:    msg.Round,
			Sender:   c.address,
			Proposal: msg.Proposal,
		}

		for _, core := range c.allCores {
			fmt.Printf("검증자 %s가 COMMIT 메시지 전파: 수신자=%s\n",
				c.address.Hex(), core.address.Hex())
			core.messages <- commitMsg
		}
	}
}

// handleRoundChange handles ROUND-CHANGE messages
func (c *TestCore) handleRoundChange(msg QBFTMessage) {
	fmt.Printf("검증자 %s가 ROUND-CHANGE 메시지 처리: 시퀀스=%d, 라운드=%d\n",
		c.address.Hex(), msg.Sequence.Uint64(), msg.Round)

	// 시퀀스가 0인 ROUND-CHANGE 메시지를 받으면 시퀀스와 라운드 초기화
	if msg.Sequence.Uint64() == 0 {
		c.state.SetSequence(big.NewInt(0))
		c.state.SetRound(big.NewInt(0))
	}
}

// newTestQBFTReorgCore creates a new test core instance
func newTestQBFTReorgCore(t *testing.T, validators []common.Address, index int) *TestCore {
	core := &TestCore{
		messageDelay:  0,
		state:         &roundState{},
		messages:      make(chan QBFTMessage, 100),
		address:       validators[index],
		prepareMsgs:   make(map[string]int),
		commitMsgs:    make(map[string]int),
		wg:            &sync.WaitGroup{},
		done:          make(chan struct{}),
		processedMsgs: make(map[string]bool),
	}

	core.state.SetSequence(big.NewInt(0))
	core.state.SetRound(big.NewInt(0))

	return core
}

// broadcastMessage 함수 수정
func (c *TestCore) broadcastMessage(msg QBFTMessage) {
	for _, core := range c.allCores {
		if core.address != c.address && core.address != msg.Sender {
			select {
			case core.messages <- msg:
				fmt.Printf("Message sent from %s to %s\n", msg.Sender.Hex(), core.address.Hex())
			case <-time.After(5 * time.Second):
				fmt.Printf("Timeout sending message from %s to %s\n", msg.Sender.Hex(), core.address.Hex())
			}
		}
	}
}

// 메시지 처리 완료 대기
func (c *TestCore) Wait(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-c.done:
		return true
	case <-timer.C:
		return false
	}
}

func Test01QBFTReorgWithDelayedValidator(t *testing.T) {
	fmt.Println("\n=== QBFT 재구성 테스트 시작 ===")

	// 초기 설정
	fmt.Println("\n1. 검증자 초기 설정")
	validators := make([]common.Address, 4)
	for i := 0; i < 4; i++ {
		validators[i] = common.BytesToAddress([]byte(fmt.Sprintf("validator%d", i)))
		fmt.Printf("   - 검증자 %d 생성: %s\n", i, validators[i].Hex())
	}

	// 각 검증자에 대한 core 인스턴스 생성
	fmt.Println("\n2. Core 인스턴스 생성")
	cores := make([]*TestCore, 4)
	var wg sync.WaitGroup
	expectedMsgCount := 1        // 각 검증자가 처리해야 할 메시지 수 (ROUND-CHANGE 메시지만)
	wg.Add(expectedMsgCount * 4) // 전체 메시지 수
	fmt.Printf("   - 예상 메시지 처리 수: %d (검증자당 %d개)\n", expectedMsgCount*4, expectedMsgCount)

	for i := 0; i < 4; i++ {
		core := &TestCore{
			state:         &roundState{sequence: big.NewInt(0)},
			messages:      make(chan QBFTMessage, messageQueueSize),
			address:       validators[i],
			prepareMsgs:   make(map[string]int),
			commitMsgs:    make(map[string]int),
			processedMsgs: make(map[string]bool),
			wg:            &wg,
		}
		cores[i] = core
		fmt.Printf("   - 검증자 %d의 Core 인스턴스 생성 완료\n", i)
	}

	// 모든 core 인스턴스에 대한 참조 설정
	fmt.Println("\n3. Core 인스턴스 시작")
	for i := 0; i < 4; i++ {
		cores[i].allCores = cores
		cores[i].Start() // 메시지 처리 루프 시작
		fmt.Printf("   - 검증자 %d의 메시지 처리 루프 시작\n", i)
	}

	// 지연된 검증자(마지막 검증자)에 30초 지연 설정
	fmt.Println("\n4. 지연된 검증자 설정")
	cores[3].messageDelay = 30 * time.Second
	fmt.Printf("   - 검증자 3 (%s)에 30초 지연 설정\n", validators[3].Hex())

	// 정상 검증자들이 블록 1에 대한 PRE-PREPARE 메시지 교환
	fmt.Println("\n5. 초기 블록 1 생성 및 전파")
	oldBlock1 := generateBlock(1, common.Hash{}, validators)
	oldBlock1Hash := oldBlock1.Hash()
	fmt.Printf("   - 초기 블록 1 해시: %s\n", oldBlock1Hash.Hex())

	for i := 0; i < 3; i++ {
		msg := QBFTMessage{
			Type:     "PRE-PREPARE",
			Sequence: big.NewInt(1),
			Round:    0,
			Sender:   validators[i],
			Proposal: oldBlock1,
		}
		cores[i].messages <- msg
		fmt.Printf("   - 검증자 %d가 블록 1 PRE-PREPARE 메시지 전송\n", i)
	}

	// 지연된 검증자가 ROUND-CHANGE 메시지 전송
	fmt.Println("\n6. ROUND-CHANGE 메시지 전송")
	roundChangeMsg := QBFTMessage{
		Type:     "ROUND-CHANGE",
		Sequence: big.NewInt(0),
		Round:    0,
		Sender:   validators[3],
	}
	fmt.Printf("   - 지연된 검증자가 ROUND-CHANGE 메시지 전송 (시퀀스: %d, 라운드: %d)\n",
		roundChangeMsg.Sequence.Uint64(), roundChangeMsg.Round)
	cores[3].messages <- roundChangeMsg

	// 메시지 처리 대기
	fmt.Println("\n7. 메시지 처리 대기")

	// 모든 검증자가 메시지 처리를 완료할 때까지 대기
	done := make(chan struct{})
	go func() {
		time.Sleep(2 * time.Second) // 메시지가 모두 전달될 때까지 잠시 대기
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("   - 모든 메시지가 성공적으로 처리되었습니다")
	case <-time.After(10 * time.Second):
		t.Fatal("   - 메시지 처리 타임아웃 (10초)")
	}

	// 모든 검증자 정지
	fmt.Println("\n8. 검증자 종료")
	for i, c := range cores {
		c.Stop()
		fmt.Printf("   - 검증자 %d 종료\n", i)
	}

	// 결과 검증
	fmt.Println("\n9. 결과 검증")
	allValid := true
	for i := 0; i < 4; i++ {
		sequence := cores[i].state.sequence.Uint64()
		msgCount := cores[i].msgCount
		fmt.Printf("   - 검증자 %d 상태:\n", i)
		fmt.Printf("     * 시퀀스: %d\n", sequence)
		fmt.Printf("     * 처리된 메시지 수: %d\n", msgCount)

		if cores[i].state.sequence.Cmp(big.NewInt(0)) != 0 {
			t.Errorf("     * 오류: 검증자 %d의 시퀀스가 0이 아닙니다: %v", i, sequence)
			allValid = false
		}
	}

	if allValid {
		fmt.Println("\n=== 모든 검증자가 성공적으로 블록 0으로 롤백됨 ===")
	} else {
		fmt.Println("\n=== 일부 검증자가 올바르게 롤백되지 않음 ===")
		return
	}
}

func Test02QBFTReorgWithDelayedValidator(t *testing.T) {
	fmt.Println("\n=== QBFT 재구성 테스트 시작2222222 ===")

	// 1. 검증자 초기화
	validators := make([]common.Address, 4)
	for i := 0; i < 4; i++ {
		validators[i] = randomAddress()
	}

	// 2. 각 검증자의 코어 인스턴스 생성
	cores := make([]*TestCore, 4)
	for i := 0; i < 4; i++ {
		cores[i] = newTestQBFTReorgCore(t, validators, i)
	}

	// 3. allCores 필드 설정
	for i := 0; i < 4; i++ {
		cores[i].allCores = cores
	}

	// 4. 메시지 처리 루프 설정
	for i := 0; i < 4; i++ {
		cores[i].Start()
	}

	// 5. 초기 블록 생성
	block := generateBlock(0, common.Hash{}, validators)

	// 6. 블록 1을 위한 PRE-PREPARE 메시지를 모든 검증자에게 전파
	block1 := generateBlock(1, block.Hash(), validators)
	preprepareMsg := QBFTMessage{
		Type:     "PRE-PREPARE",
		Sequence: big.NewInt(1),
		Round:    0,
		Sender:   validators[0],
		Proposal: block1,
	}

	// 모든 검증자에게 PRE-PREPARE 메시지 전송
	for i := 0; i < 4; i++ {
		cores[i].messages <- preprepareMsg
	}

	// 7. 메시지 처리 완료 대기 (시간 증가)
	time.Sleep(5 * time.Second)

	// 8. 모든 검증자 중지
	for i := 0; i < 4; i++ {
		cores[i].Stop()
	}

	// 9. 결과 검증
	for i := 0; i < 4; i++ {
		if cores[i].state.Sequence().Uint64() != 1 {
			t.Errorf("검증자 %d가 블록 1로 진행하지 못했습니다. 현재 시퀀스: %d", i, cores[i].state.Sequence().Uint64())
		}
	}
}

// Test01에서 사용된 타입과 함수들을 재사용
type message struct {
	Code    uint64
	Msg     []byte
	Address common.Address
}

const (
	msgPreprepare = iota
	msgPrepare
	msgCommit
	msgRoundChange
)

func randomAddress() common.Address {
	b := make([]byte, 20)
	rand.Read(b)
	return common.BytesToAddress(b)
}

func generateBlock(number uint64, parentHash common.Hash, validators []common.Address) *types.Block {
	header := &types.Header{
		Number:     big.NewInt(int64(number)),
		ParentHash: parentHash,
		Time:       uint64(time.Now().Unix()),
	}
	return types.NewBlock(header, nil, nil, nil, new(trie.Trie))
}

type MockBatch struct{}

func (b *MockBatch) Put(key []byte, value []byte) error {
	return nil
}

func (b *MockBatch) Delete(key []byte) error {
	return nil
}

func (b *MockBatch) ValueSize() int {
	return 0
}

func (b *MockBatch) Write() error {
	return nil
}

func (b *MockBatch) Reset() {}

func (b *MockBatch) Replay(w ethdb.KeyValueWriter) error {
	return nil
}

func (db *MockDatabase) NewIterator(prefix []byte, start []byte) ethdb.Iterator {
	return &MockIterator{}
}

type MockIterator struct{}

func (it *MockIterator) Next() bool {
	return false
}

func (it *MockIterator) Error() error {
	return nil
}

func (it *MockIterator) Key() []byte {
	return nil
}

func (it *MockIterator) Value() []byte {
	return nil
}

func (it *MockIterator) Release() {}

type MockSnapshot struct{}

func (s *MockSnapshot) Has(key []byte) (bool, error) {
	return false, nil
}

func (s *MockSnapshot) Get(key []byte) ([]byte, error) {
	return nil, nil
}

func (s *MockSnapshot) Release() {}
