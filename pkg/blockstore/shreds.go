package blockstore

import (
	"encoding/binary"
	"fmt"

	"github.com/certusone/radiance/pkg/shred"
	bin "github.com/gagliardetto/binary"
	"github.com/linxGnu/grocksdb"
	"golang.org/x/exp/constraints"
)

// MakeShredKey creates the RocksDB key for CfDataShred or CfCodeShred.
func MakeShredKey(slot, index uint64) (key [16]byte) {
	binary.BigEndian.PutUint64(key[0:8], slot)
	binary.BigEndian.PutUint64(key[8:16], index)
	return
}

// ParseShredKey decodes the RocksDB keys in CfDataShred or CfCodeShred.
func ParseShredKey(key []byte) (slot uint64, index uint64, ok bool) {
	ok = len(key) == 16
	if !ok {
		return
	}
	slot = binary.BigEndian.Uint64(key[0:8])
	index = binary.BigEndian.Uint64(key[8:16])
	return
}

type entryRange struct {
	startIdx, endIdx uint32
}

// entryRanges returns the shred ranges of each entry
func (s *SlotMeta) entryRanges() []entryRange {
	if !s.IsFull() {
		return nil
	}
	indexes := sliceSortedByRange[uint32](s.EntryEndIndexes, 0, uint32(s.Consumed))
	ranges := make([]entryRange, len(indexes))
	begin := uint32(0)
	for i, index := range s.EntryEndIndexes {
		ranges[i] = entryRange{begin, index}
		begin = index + 1
	}
	return ranges
}

func sliceSortedByRange[T constraints.Ordered](list []T, start T, stop T) []T {
	for len(list) > 0 && list[0] < start {
		list = list[1:]
	}
	for len(list) > 0 && list[len(list)-1] >= stop {
		list = list[:len(list)-1]
	}
	return list
}

type Entries struct {
	Entries []shred.Entry
	Raw     []byte
	Shreds  []shred.Shred
}

func (d *DB) GetEntries(meta *SlotMeta) ([]Entries, error) {
	shreds, err := d.GetDataShreds(meta.Slot, 0, uint32(meta.Received))
	if err != nil {
		return nil, err
	}
	return DataShredsToEntries(meta, shreds)
}

// DataShredsToEntries reassembles shreds to entries containing transactions.
func DataShredsToEntries(meta *SlotMeta, shreds []shred.Shred) (entries []Entries, err error) {
	ranges := meta.entryRanges()
	for _, r := range ranges {
		parts := shreds[r.startIdx : r.endIdx+1]
		entryBytes, err := shred.Concat(parts)
		if err != nil {
			return nil, err
		}
		dec := bin.NewBinDecoder(entryBytes)
		var subEntries struct {
			NumEntries uint64 `bin:"sizeof=Entries"`
			Entries    []shred.Entry
		}
		if err := dec.Decode(&subEntries); err != nil {
			return nil, fmt.Errorf("cannot decode entry at %d:[%d-%d]: %w",
				meta.Slot, r.startIdx, r.endIdx, err)
		}
		entries = append(entries, Entries{
			Entries: subEntries.Entries,
			Raw:     entryBytes[:dec.Position()],
			Shreds:  parts,
		})
	}
	return entries, nil
}

func (d *DB) GetAllDataShreds(slot uint64) ([]shred.Shred, error) {
	return d.getAllShreds(d.CfDataShred, slot)
}

func (d *DB) GetDataShreds(slot uint64, startIdx, endIdx uint32) ([]shred.Shred, error) {
	iter := d.DB.NewIteratorCF(grocksdb.NewDefaultReadOptions(), d.CfDataShred)
	defer iter.Close()
	key := MakeShredKey(slot, uint64(startIdx))
	iter.Seek(key[:])
	var shreds []shred.Shred
	for i := startIdx; i < endIdx; i++ {
		var curSlot, index uint64
		valid := iter.Valid()
		if valid {
			key := iter.Key().Data()
			if len(key) != 16 {
				continue
			}
			curSlot = binary.BigEndian.Uint64(key)
			index = binary.BigEndian.Uint64(key[8:])
		}
		if !valid || curSlot != slot {
			return nil, fmt.Errorf("missing shreds for slot %d", slot)
		}
		if index != uint64(i) {
			return nil, fmt.Errorf("missing shred %d for slot %d", i, index)
		}
		s := shred.NewShredFromSerialized(iter.Value().Data())
		if s == nil {
			return nil, fmt.Errorf("failed to deserialize shred %d/%d", slot, i)
		}
		shreds = append(shreds, s)
		iter.Next()
	}
	return shreds, nil
}

func (d *DB) GetDataShred(slot, index uint64) (shred.Shred, error) {
	return d.getShred(d.CfDataShred, slot, index)
}

func (d *DB) GetRawDataShred(slot, index uint64) (*grocksdb.Slice, error) {
	return d.getRawShred(d.CfDataShred, slot, index)
}

func (d *DB) GetAllCodeShreds(slot uint64) ([]shred.Shred, error) {
	return d.getAllShreds(d.CfDataShred, slot)
}

func (d *DB) GetCodeShred(slot, index uint64) (shred.Shred, error) {
	return d.getShred(d.CfCodeShred, slot, index)
}

func (d *DB) GetRawCodeShred(slot, index uint64) (*grocksdb.Slice, error) {
	return d.getRawShred(d.CfCodeShred, slot, index)
}

func (d *DB) getRawShred(
	cf *grocksdb.ColumnFamilyHandle,
	slot, index uint64,
) (*grocksdb.Slice, error) {
	opts := grocksdb.NewDefaultReadOptions()
	key := MakeShredKey(slot, index)
	return d.DB.GetCF(opts, cf, key[:])
}

func (d *DB) getShred(
	cf *grocksdb.ColumnFamilyHandle,
	slot, index uint64,
) (shred.Shred, error) {
	value, err := d.getRawShred(cf, slot, index)
	if err != nil {
		return nil, err
	}
	defer value.Free()
	s := shred.NewShredFromSerialized(value.Data())
	return s, nil
}

func (d *DB) getAllShreds(
	cf *grocksdb.ColumnFamilyHandle,
	slot uint64,
) ([]shred.Shred, error) {
	iter := d.DB.NewIteratorCF(grocksdb.NewDefaultReadOptions(), cf)
	defer iter.Close()
	prefix := MakeShredKey(slot, 0)
	iter.Seek(prefix[:])
	var shreds []shred.Shred
	for iter.ValidForPrefix(prefix[:8]) {
		s := shred.NewShredFromSerialized(iter.Value().Data())
		if s != nil {
			shreds = append(shreds, s)
		}
		iter.Next()
	}
	return shreds, nil
}