// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package types

import (
	"container/heap"
	"errors"
	"io"
	"math/big"
	"sync/atomic"
	"os"

	"github.com/MetisProtocol/l2geth/common"
	"github.com/MetisProtocol/l2geth/common/hexutil"
	"github.com/MetisProtocol/l2geth/crypto"
	"github.com/MetisProtocol/l2geth/rlp"
	"github.com/MetisProtocol/l2geth/rollup/fees"
)

//go:generate gencodec -type txdata -field-override txdataMarshaling -out gen_tx_json.go

var (
	ErrInvalidSig = errors.New("invalid transaction v, r, s values")
)

type Transaction struct {
	data txdata
	meta TransactionMeta
	// caches
	hash atomic.Value
	size atomic.Value
	from atomic.Value
}

type txdata struct {
	AccountNonce uint64          `json:"nonce"    gencodec:"required"`
	Price        *big.Int        `json:"gasPrice" gencodec:"required"`
	GasLimit     uint64          `json:"gas"      gencodec:"required"`
	Recipient    *common.Address `json:"to"       rlp:"nil"` // nil means contract creation
	Amount       *big.Int        `json:"value"    gencodec:"required"`
	Payload      []byte          `json:"input"    gencodec:"required"`

	// Signature values
	V *big.Int `json:"v" gencodec:"required"`
	R *big.Int `json:"r" gencodec:"required"`
	S *big.Int `json:"s" gencodec:"required"`

	// This is only used when marshaling to JSON.
	Hash *common.Hash `json:"hash" rlp:"-"`

	// NOTE 20210724
	// L1Info
	// L1BlockNumber     *big.Int          `json:"l1BlockNumber" rlp:"0"`
	// L1Timestamp       uint64            `json:"l1Timestamp" rlp:"0"`
	// L1MessageSender   *common.Address   `json:"L1MessageSender" rlp:"nil"`
	// QueueOrigin       *big.Int          `json:"queueOrigin" rlp:"0"`
	// // The canonical transaction chain index
	// Index *uint64 `json:"index" rlp:"0"`
	// // The queue index, nil for queue origin sequencer transactions
	// QueueIndex *uint64 `json:"queueIndex" rlp:"0"`
}

type txdataMarshaling struct {
	AccountNonce hexutil.Uint64
	Price        *hexutil.Big
	GasLimit     hexutil.Uint64
	Amount       *hexutil.Big
	Payload      hexutil.Bytes
	V            *hexutil.Big
	R            *hexutil.Big
	S            *hexutil.Big
	// NOTE 20210724
	// L1BlockNumber     *hexutil.Big
	// L1Timestamp       hexutil.Uint64
	// QueueOrigin       *hexutil.Big
	// Index             *hexutil.Uint64
	// QueueIndex        *hexutil.Uint64
}

func NewTransaction(nonce uint64, to common.Address, amount *big.Int, gasLimit uint64, gasPrice *big.Int, data []byte) *Transaction {
	return newTransaction(nonce, &to, amount, gasLimit, gasPrice, data)
}

func NewContractCreation(nonce uint64, amount *big.Int, gasLimit uint64, gasPrice *big.Int, data []byte) *Transaction {
	return newTransaction(nonce, nil, amount, gasLimit, gasPrice, data)
}

func newTransaction(nonce uint64, to *common.Address, amount *big.Int, gasLimit uint64, gasPrice *big.Int, data []byte) *Transaction {
	if len(data) > 0 {
		data = common.CopyBytes(data)
	}

	meta := NewTransactionMeta(nil, 0, nil, QueueOriginSequencer, nil, nil, nil)

	d := txdata{
		AccountNonce: nonce,
		Recipient:    to,
		Payload:      data,
		Amount:       new(big.Int),
		GasLimit:     gasLimit,
		Price:        new(big.Int),
		V:            new(big.Int),
		R:            new(big.Int),
		S:            new(big.Int),
		// NOTE 20210724
		// L1BlockNumber:     new(big.Int),
		// L1Timestamp:       0,
		// QueueOrigin:       big.NewInt(int64(QueueOriginSequencer)),
		// Index:             &index1,
		// QueueIndex:        &index1,
	}
	if amount != nil {
		d.Amount.Set(amount)
	}
	if gasPrice != nil {
		d.Price.Set(gasPrice)
	}

	return &Transaction{data: d, meta: *meta}
}

func (t *Transaction) SetTransactionMeta(meta *TransactionMeta) {
	if meta == nil {
		return
	}
	t.meta = *meta

	// NOTE 20210724
	// t.data.L1BlockNumber = t.meta.L1BlockNumber
	// t.data.L1Timestamp = t.meta.L1Timestamp
	// t.data.QueueOrigin = t.meta.QueueOrigin
	// t.data.Index = t.meta.Index
	// t.data.QueueIndex = t.meta.QueueIndex

	// t.data.L1MessageSender = t.meta.L1MessageSender
}

func (t *Transaction) GetMeta() *TransactionMeta {
	if &t.meta == nil {
		return nil
	}
	return &t.meta
}
func (t *Transaction) SetIndex(index uint64) {
	if &t.meta == nil {
		return
	}
	t.meta.Index = &index

	// NOTE 20210724
	// t.data.Index = t.meta.Index
}

func (t *Transaction) SetL1Timestamp(ts uint64) {
	if &t.meta == nil {
		return
	}
	t.meta.L1Timestamp = ts

	// NOTE 20210724
	// t.data.L1Timestamp = t.meta.L1Timestamp
}

func (t *Transaction) L1Timestamp() uint64 {
	if &t.meta == nil {
		return 0
	}
	return t.meta.L1Timestamp
}

func (t *Transaction) SetL1BlockNumber(bn uint64) {
	if &t.meta == nil {
		return
	}
	t.meta.L1BlockNumber = new(big.Int).SetUint64(bn)

	// NOTE 20210724
	// t.data.L1BlockNumber = t.meta.L1BlockNumber
}

// ChainId returns which chain id this transaction was signed for (if at all)
func (tx *Transaction) ChainId() *big.Int {
	return deriveChainId(tx.data.V)
}

// Protected returns whether the transaction is protected from replay protection.
func (tx *Transaction) Protected() bool {
	return isProtectedV(tx.data.V)
}

func isProtectedV(V *big.Int) bool {
	if V.BitLen() <= 8 {
		v := V.Uint64()
		return v != 27 && v != 28
	}
	// anything not 27 or 28 is considered protected
	return true
}

// EncodeRLP implements rlp.Encoder
func (tx *Transaction) EncodeRLP(w io.Writer) error {
	return rlp.Encode(w, &tx.data)
}

// DecodeRLP implements rlp.Decoder
func (tx *Transaction) DecodeRLP(s *rlp.Stream) error {
	_, size, _ := s.Kind()
	err := s.Decode(&tx.data)
	if err == nil {
		tx.size.Store(common.StorageSize(rlp.ListSize(size)))
	}

	return err
}

// MarshalJSON encodes the web3 RPC transaction format.
func (tx *Transaction) MarshalJSON() ([]byte, error) {
	return tx.data.MarshalJSON()
}

// UnmarshalJSON decodes the web3 RPC transaction format.
func (tx *Transaction) UnmarshalJSON(input []byte) error {
	err := tx.data.UnmarshalJSON(input)
	if err != nil {
		return err
	}

	withSignature := tx.data.V.Sign() != 0 || tx.data.R.Sign() != 0 || tx.data.S.Sign() != 0
	if withSignature {
		var V byte
		if isProtectedV(tx.data.V) {
			chainID := deriveChainId(tx.data.V).Uint64()
			V = byte(tx.data.V.Uint64() - 35 - 2*chainID)
		} else {
			V = byte(tx.data.V.Uint64() - 27)
		}
		if !crypto.ValidateSignatureValues(V, tx.data.R, tx.data.S, false) {
			return ErrInvalidSig
		}
	}

	return nil
}

func (tx *Transaction) Data() []byte       { return common.CopyBytes(tx.data.Payload) }
func (tx *Transaction) Gas() uint64        { return tx.data.GasLimit }
func (tx *Transaction) L2Gas() uint64      { return fees.DecodeL2GasLimitU64(tx.data.GasLimit) }
func (tx *Transaction) GasPrice() *big.Int { return new(big.Int).Set(tx.data.Price) }
func (tx *Transaction) Value() *big.Int    { return new(big.Int).Set(tx.data.Amount) }
func (tx *Transaction) Nonce() uint64      { return tx.data.AccountNonce }
func (tx *Transaction) CheckNonce() bool   { return true }

func (tx *Transaction) SetNonce(nonce uint64) { tx.data.AccountNonce = nonce }

// To returns the recipient address of the transaction.
// It returns nil if the transaction is a contract creation.
func (tx *Transaction) To() *common.Address {
	if tx.data.Recipient == nil {
		return nil
	}
	to := *tx.data.Recipient
	return &to
}

// L1MessageSender returns the L1 message sender address of the transaction if one exists.
// It returns nil if this transaction was not from an L1 contract.
func (tx *Transaction) L1MessageSender() *common.Address {
	if tx.meta.L1MessageSender == nil {
		return nil
	}
	l1MessageSender := *tx.meta.L1MessageSender
	return &l1MessageSender
}

// L1BlockNumber returns the L1 block number of the transaction if one exists.
// It returns nil if this transaction was not generated from a transaction received on L1.
func (tx *Transaction) L1BlockNumber() *big.Int {
	if tx.meta.L1BlockNumber == nil {
		return nil
	}
	l1BlockNumber := *tx.meta.L1BlockNumber
	return &l1BlockNumber
}

// QueueOrigin returns the Queue Origin of the transaction
func (tx *Transaction) QueueOrigin() QueueOrigin {
	return tx.meta.QueueOrigin
}

// Hash hashes the RLP encoding of tx.
// It uniquely identifies the transaction.
func (tx *Transaction) Hash() common.Hash {
	if hash := tx.hash.Load(); hash != nil {
		return hash.(common.Hash)
	}

	v := rlpHash(tx)
	tx.hash.Store(v)
	return v
}

// Size returns the true RLP encoded storage size of the transaction, either by
// encoding and returning it, or returning a previsouly cached value.
func (tx *Transaction) Size() common.StorageSize {
	if size := tx.size.Load(); size != nil {
		return size.(common.StorageSize)
	}
	c := writeCounter(0)
	rlp.Encode(&c, &tx.data)
	tx.size.Store(common.StorageSize(c))
	return common.StorageSize(c)
}

// AsMessage returns the transaction as a core.Message.
//
// AsMessage requires a signer to derive the sender.
//
// XXX Rename message to something less arbitrary?
func (tx *Transaction) AsMessage(s Signer) (Message, error) {
	// TOOD 20210724
	txMeta := tx.GetMeta()
	// if tx.data.V.Cmp(big.NewInt(0)) == 0 {
	// 	txMeta.L1BlockNumber = big.NewInt(0)
	// 	txMeta.L1Timestamp = 0
	// 	l1 := common.HexToAddress(os.Getenv("ETH1_L1_CROSS_DOMAIN_MESSENGER_ADDRESS"))
	// 	txMeta.L1MessageSender = &l1
	// 	txMeta.QueueOrigin = big.NewInt(int64(QueueOriginL1ToL2))
	// 	index1 := uint64(0)
	// 	txMeta.Index = &index1
	// 	qindex1 := uint64(0)
	// 	txMeta.QueueIndex = &qindex1
	// 	txMeta.RawTransaction = tx.data.Payload
	// }
	// if txMeta.QueueOrigin == nil {
	// 	txMeta.L1BlockNumber = big.NewInt(0)
	// 	txMeta.L1Timestamp = 0
	// 	txMeta.L1MessageSender = nil
	// 	txMeta.QueueOrigin = big.NewInt(int64(QueueOriginSequencer))
	// 	index1 := uint64(0)
	// 	txMeta.Index = &index1
	// 	qindex1 := uint64(0)
	// 	txMeta.QueueIndex = &qindex1
	// 	txMeta.RawTransaction = tx.data.Payload
	// }
	if tx.data.V.Cmp(big.NewInt(0)) == 0 {
		// L1 message
		txMeta.L1BlockNumber = big.NewInt(0)
		txMeta.L1Timestamp = 0
		l1 := common.HexToAddress(os.Getenv("ETH1_L1_CROSS_DOMAIN_MESSENGER_ADDRESS"))
		txMeta.L1MessageSender = &l1
		txMeta.QueueOrigin = QueueOriginL1ToL2
		index1 := uint64(0)
		txMeta.Index = &index1
		qindex1 := uint64(0)
		txMeta.QueueIndex = &qindex1
		txMeta.RawTransaction = tx.data.Payload
	} else {
		txMeta.L1BlockNumber = big.NewInt(0)
		if &txMeta.L1Timestamp == nil {
		 	txMeta.L1Timestamp = 0
		}
		// txMeta.L1MessageSender = nil
		txMeta.QueueOrigin = QueueOriginSequencer
		if txMeta.Index == nil {
			index1 := uint64(0)
			txMeta.Index = &index1
		}
		if txMeta.QueueIndex == nil {
			qindex1 := uint64(0)
			txMeta.QueueIndex = &qindex1
		}
		txMeta.RawTransaction = tx.data.Payload

		// txMeta.L1Timestamp = tx.data.L1Timestamp
		// txMeta.L1BlockNumber = tx.data.L1BlockNumber
		// txMeta.Index = tx.data.Index
		// txMeta.QueueIndex = tx.data.QueueIndex
		// txMeta.QueueOrigin = tx.data.QueueOrigin
	}
	// txMeta.L1Timestamp = tx.data.L1Timestamp
	// txMeta.L1BlockNumber = tx.data.L1BlockNumber
	// txMeta.Index = tx.data.Index
	// txMeta.QueueIndex = tx.data.QueueIndex
	// txMeta.L1MessageSender = tx.data.L1MessageSender
	tx.SetTransactionMeta(txMeta)

	msg := Message{
		nonce:      tx.data.AccountNonce,
		gasLimit:   tx.data.GasLimit,
		gasPrice:   new(big.Int).Set(tx.data.Price),
		to:         tx.data.Recipient,
		amount:     tx.data.Amount,
		data:       tx.data.Payload,
		checkNonce: true,

		l1MessageSender: tx.meta.L1MessageSender,
		l1BlockNumber:   tx.meta.L1BlockNumber,
		queueOrigin:       tx.meta.QueueOrigin,

		// NOTE 20210724
		l1Timestamp: tx.meta.L1Timestamp,
		// index:       tx.meta.Index,
		// queueIndex:  tx.meta.QueueIndex,

	}

	var err error
	msg.from, err = Sender(s, tx)

	if tx.meta.L1MessageSender != nil {
		msg.l1MessageSender = tx.meta.L1MessageSender
	} else {
		addr := common.Address{}
		msg.l1MessageSender = &addr
	}

	return msg, err
}

// WithSignature returns a new transaction with the given signature.
// This signature needs to be in the [R || S || V] format where V is 0 or 1.
func (tx *Transaction) WithSignature(signer Signer, sig []byte) (*Transaction, error) {
	r, s, v, err := signer.SignatureValues(tx, sig)
	if err != nil {
		return nil, err
	}
	cpy := &Transaction{data: tx.data, meta: tx.meta}
	cpy.data.R, cpy.data.S, cpy.data.V = r, s, v
	return cpy, nil
}

// Cost returns amount + gasprice * gaslimit.
func (tx *Transaction) Cost() *big.Int {
	total := new(big.Int).Mul(tx.data.Price, new(big.Int).SetUint64(tx.data.GasLimit))
	total.Add(total, tx.data.Amount)
	return total
}

// RawSignatureValues returns the V, R, S signature values of the transaction.
// The return values should not be modified by the caller.
func (tx *Transaction) RawSignatureValues() (v, r, s *big.Int) {
	return tx.data.V, tx.data.R, tx.data.S
}

// Transactions is a Transaction slice type for basic sorting.
type Transactions []*Transaction

// Len returns the length of s.
func (s Transactions) Len() int { return len(s) }

// Swap swaps the i'th and the j'th element in s.
func (s Transactions) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

// GetRlp implements Rlpable and returns the i'th element of s in rlp.
func (s Transactions) GetRlp(i int) []byte {
	enc, _ := rlp.EncodeToBytes(s[i])
	return enc
}

// TxDifference returns a new set which is the difference between a and b.
func TxDifference(a, b Transactions) Transactions {
	keep := make(Transactions, 0, len(a))

	remove := make(map[common.Hash]struct{})
	for _, tx := range b {
		remove[tx.Hash()] = struct{}{}
	}

	for _, tx := range a {
		if _, ok := remove[tx.Hash()]; !ok {
			keep = append(keep, tx)
		}
	}

	return keep
}

// TxByNonce implements the sort interface to allow sorting a list of transactions
// by their nonces. This is usually only useful for sorting transactions from a
// single account, otherwise a nonce comparison doesn't make much sense.
type TxByNonce Transactions

func (s TxByNonce) Len() int           { return len(s) }
func (s TxByNonce) Less(i, j int) bool { return s[i].data.AccountNonce < s[j].data.AccountNonce }
func (s TxByNonce) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

// TxByPrice implements both the sort and the heap interface, making it useful
// for all at once sorting as well as individually adding and removing elements.
type TxByPrice Transactions

func (s TxByPrice) Len() int           { return len(s) }
func (s TxByPrice) Less(i, j int) bool { return s[i].data.Price.Cmp(s[j].data.Price) > 0 }
func (s TxByPrice) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (s *TxByPrice) Push(x interface{}) {
	*s = append(*s, x.(*Transaction))
}

func (s *TxByPrice) Pop() interface{} {
	old := *s
	n := len(old)
	x := old[n-1]
	*s = old[0 : n-1]
	return x
}

// TransactionsByPriceAndNonce represents a set of transactions that can return
// transactions in a profit-maximizing sorted order, while supporting removing
// entire batches of transactions for non-executable accounts.
type TransactionsByPriceAndNonce struct {
	txs    map[common.Address]Transactions // Per account nonce-sorted list of transactions
	heads  TxByPrice                       // Next transaction for each unique account (price heap)
	signer Signer                          // Signer for the set of transactions
}

// NewTransactionsByPriceAndNonce creates a transaction set that can retrieve
// price sorted transactions in a nonce-honouring way.
//
// Note, the input map is reowned so the caller should not interact any more with
// if after providing it to the constructor.
func NewTransactionsByPriceAndNonce(signer Signer, txs map[common.Address]Transactions) *TransactionsByPriceAndNonce {
	// Initialize a price based heap with the head transactions
	heads := make(TxByPrice, 0, len(txs))
	for from, accTxs := range txs {
		// This prevents a panic, not ideal.
		if len(accTxs) > 0 {
			heads = append(heads, accTxs[0])
			// Ensure the sender address is from the signer
			acc, _ := Sender(signer, accTxs[0])
			txs[acc] = accTxs[1:]
			if from != acc {
				delete(txs, from)
			}
		}
	}
	heap.Init(&heads)

	// Assemble and return the transaction set
	return &TransactionsByPriceAndNonce{
		txs:    txs,
		heads:  heads,
		signer: signer,
	}
}

// Peek returns the next transaction by price.
func (t *TransactionsByPriceAndNonce) Peek() *Transaction {
	if len(t.heads) == 0 {
		return nil
	}
	return t.heads[0]
}

// Shift replaces the current best head with the next one from the same account.
func (t *TransactionsByPriceAndNonce) Shift() {
	acc, _ := Sender(t.signer, t.heads[0])
	if txs, ok := t.txs[acc]; ok && len(txs) > 0 {
		t.heads[0], t.txs[acc] = txs[0], txs[1:]
		heap.Fix(&t.heads, 0)
	} else {
		heap.Pop(&t.heads)
	}
}

// Pop removes the best transaction, *not* replacing it with the next one from
// the same account. This should be used when a transaction cannot be executed
// and hence all subsequent ones should be discarded from the same account.
func (t *TransactionsByPriceAndNonce) Pop() {
	heap.Pop(&t.heads)
}

// Message is a fully derived transaction and implements core.Message
//
// NOTE: In a future PR this will be removed.
type Message struct {
	to         *common.Address
	from       common.Address
	nonce      uint64
	amount     *big.Int
	gasLimit   uint64
	gasPrice   *big.Int
	data       []byte
	checkNonce bool

	l1Timestamp     uint64
	l1BlockNumber   *big.Int
	l1MessageSender *common.Address
	queueOrigin     QueueOrigin
}

func NewMessage(from common.Address, to *common.Address, nonce uint64, amount *big.Int, gasLimit uint64, gasPrice *big.Int, data []byte, checkNonce bool, l1MessageSender *common.Address, l1BlockNumber *big.Int, queueOrigin QueueOrigin) Message {
	return Message{
		from:       from,
		to:         to,
		nonce:      nonce,
		amount:     amount,
		gasLimit:   gasLimit,
		gasPrice:   gasPrice,
		data:       data,
		checkNonce: checkNonce,
		l1BlockNumber:   l1BlockNumber,
		l1MessageSender: l1MessageSender,
		queueOrigin:     queueOrigin,


		// TODO 20200724
		l1Timestamp: 0,
		// index:       &index1,
		// queueIndex:  &index1,
	}
}

// NOTE 20210724
// func NewMessage2(from common.Address, to *common.Address, nonce uint64, amount *big.Int, gasLimit uint64, gasPrice *big.Int, data []byte, checkNonce bool, l1MessageSender *common.Address, l1BlockNumber *big.Int, queueOrigin QueueOrigin, signatureHashType SignatureHashType, l1Timestamp uint64, index *uint64, queueIndex *uint64) Message {
// 	return Message{
// 		from:       from,
// 		to:         to,
// 		nonce:      nonce,
// 		amount:     amount,
// 		gasLimit:   gasLimit,
// 		gasPrice:   gasPrice,
// 		data:       data,
// 		checkNonce: checkNonce,

// 		l1BlockNumber:     l1BlockNumber,
// 		l1MessageSender:   l1MessageSender,
// 		signatureHashType: signatureHashType,
// 		queueOrigin:       big.NewInt(int64(queueOrigin)),

// 		l1Timestamp: l1Timestamp,
// 		index:       index,
// 		queueIndex:  queueIndex,
// 	}
// }

func (m Message) From() common.Address { return m.from }
func (m Message) To() *common.Address  { return m.to }
func (m Message) GasPrice() *big.Int   { return m.gasPrice }
func (m Message) Value() *big.Int      { return m.amount }
func (m Message) Gas() uint64          { return m.gasLimit }
func (m Message) Nonce() uint64        { return m.nonce }
func (m Message) Data() []byte         { return m.data }
func (m Message) CheckNonce() bool     { return m.checkNonce }

func (m Message) L1MessageSender() *common.Address { return m.l1MessageSender }
func (m Message) L1BlockNumber() *big.Int          { return m.l1BlockNumber }
func (m Message) QueueOrigin() QueueOrigin         { return m.queueOrigin }

// NOTE 20210724
func (m Message) L1Timestamp() uint64 { return m.l1Timestamp }
// func (m Message) Index() *uint64      { return m.index }
// func (m Message) QueueIndex() *uint64 { return m.queueIndex }
