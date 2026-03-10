package table

import (
	"cmp"
	"slices"
	"time"
)

// SortRows returns a new slice of rows sorted by the named column's SortKey.
// The input slice is not modified. Unknown column names return the input unchanged.
// Nil sort keys sort last. The sort is stable.
func SortRows[T any](rows []Row[T], columns []Column[T], colName string, ascending bool) []Row[T] {
	colIdx := -1
	for i, col := range columns {
		if col.Name == colName {
			colIdx = i
			break
		}
	}
	if colIdx < 0 {
		return rows
	}

	result := make([]Row[T], len(rows))
	copy(result, rows)

	slices.SortStableFunc(result, func(a, b Row[T]) int {
		aKey := cellSortKey(a, colIdx)
		bKey := cellSortKey(b, colIdx)

		// nil sorts last.
		if aKey == nil && bKey == nil {
			return 0
		}
		if aKey == nil {
			return 1
		}
		if bKey == nil {
			return -1
		}

		c := compareSortKeys(aKey, bKey)
		if !ascending {
			c = -c
		}
		return c
	})

	return result
}

func cellSortKey[T any](row Row[T], colIdx int) any {
	if colIdx < len(row.Cells) {
		return row.Cells[colIdx].SortKey
	}
	return nil
}

func compareSortKeys(a, b any) int {
	switch av := a.(type) {
	case string:
		if bv, ok := b.(string); ok {
			return cmp.Compare(av, bv)
		}
	case time.Time:
		if bv, ok := b.(time.Time); ok {
			return av.Compare(bv)
		}
	case int:
		if bv, ok := b.(int); ok {
			return cmp.Compare(av, bv)
		}
	}
	return 0
}
