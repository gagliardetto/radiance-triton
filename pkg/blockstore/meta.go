package blockstore

import (
	"encoding/binary"
	"math"
)

// SlotMeta is data stored in CfMeta
type SlotMeta struct {
	Slot                uint64   `yaml:"-"`
	Consumed            uint64   `yaml:"consumed"`
	Received            uint64   `yaml:"received"`
	FirstShredTimestamp uint64   `yaml:"first_shred_timestamp"`
	LastIndex           uint64   `yaml:"last_index"`  // optional, None being math.MaxUint64
	ParentSlot          uint64   `yaml:"parent_slot"` // optional, None being math.MaxUint64
	NumNextSlots        uint64   `bin:"sizeof=NextSlots" yaml:"-"`
	NextSlots           []uint64 `yaml:"next_slots,flow"`
	IsConnected         bool     `yaml:"is_connected"`
	// shred indexes that mark the end of an entry (used for shreds => entry mapping)
	NumEntryEndIndexes uint64   `bin:"sizeof=EntryEndIndexes" yaml:"-"`
	EntryEndIndexes    []uint32 `yaml:"completed_data_indexes,flow"`
}

// MakeSlotKey creates the RocksDB key for CfMeta, CfRoot.
func MakeSlotKey(slot uint64) (key [8]byte) {
	binary.BigEndian.PutUint64(key[0:8], slot)
	return
}

// ParseSlotKey decodes the RocksDB keys in CfMeta, CfRoot.
func ParseSlotKey(key []byte) (slot uint64, ok bool) {
	ok = len(key) == 8
	if !ok {
		return
	}
	slot = binary.BigEndian.Uint64(key)
	return
}

func (s *SlotMeta) IsFull() bool {
	// last_index is math.MaxUint64 when it has no information
	// about how many shreds will fill this slot.
	if s.LastIndex == math.MaxUint64 {
		return false
	}
	return s.Consumed == s.LastIndex+1
}
