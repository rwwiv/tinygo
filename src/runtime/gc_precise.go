//go:build gc.precise

// This implements the block-based GC as a partially precise GC. This means that
// for most heap allocations it is known which words contain a pointer and which
// don't. This should in theory make the GC faster (because it can skip
// non-pointer object) and have fewer false positives in a GC cycle. It does
// however use a bit more RAM to store the layout of each object.

package runtime

import "unsafe"

const preciseHeap = true

type gcObjectScanner struct {
	index      uintptr
	size       uintptr
	bitmap     uintptr
	bitmapAddr unsafe.Pointer
}

func newGCObjectScanner(block gcBlock) gcObjectScanner {
	if gcAsserts && block != block.findHead() {
		runtimePanic("gc: object scanner must start at head")
	}
	scanner := gcObjectScanner{}
	layout := *(*uintptr)(unsafe.Pointer(block.address()))
	if layout == 0 {
		// Unknown layout. Assume all words in the object could be pointers.
		scanner.size = 1
		scanner.bitmap = 1
	} else if layout&1 != 0 {
		// Layout is stored directly in the integer value.
		// Determine format of bitfields in the integer.
		const layoutBits = uint64(unsafe.Sizeof(layout) * 8)
		var sizeFieldBits uint64
		switch layoutBits { // note: this switch should be resolved at compile time
		case 16:
			sizeFieldBits = 4
		case 32:
			sizeFieldBits = 5
		case 64:
			sizeFieldBits = 6
		default:
			runtimePanic("unknown pointer size")
		}

		// Extract fields.
		scanner.size = (layout >> 1) & (1<<sizeFieldBits - 1)
		scanner.bitmap = layout >> (1 + sizeFieldBits)
	} else {
		// Layout is stored separately in a global object.
		layoutAddr := unsafe.Pointer(layout)
		scanner.size = *(*uintptr)(layoutAddr)
		scanner.bitmapAddr = unsafe.Add(layoutAddr, unsafe.Sizeof(uintptr(0)))
	}
	return scanner
}

func (scanner *gcObjectScanner) pointerFree() bool {
	if scanner.bitmapAddr != nil {
		// While the format allows for large objects without pointers, this is
		// optimized by the compiler so if bitmapAddr is set, we know that there
		// are at least some pointers in the object.
		return false
	}
	// If the bitmap is zero, there are definitely no pointers in the object.
	return scanner.bitmap == 0
}

func (scanner *gcObjectScanner) nextIsPointer(word, parent, addrOfWord uintptr) bool {
	index := scanner.index
	scanner.index++
	if scanner.index == scanner.size {
		scanner.index = 0
	}

	if !isOnHeap(word) {
		// Definitely isn't a pointer.
		return false
	}

	// Might be a pointer. Now look at the object layout to know for sure.
	if scanner.bitmapAddr != nil {
		if (*(*uint8)(unsafe.Add(scanner.bitmapAddr, index/8))>>(index%8))&1 == 0 {
			return false
		}
		return true
	}
	if (scanner.bitmap>>index)&1 == 0 {
		// not a pointer!
		return false
	}

	// Probably a pointer.
	return true
}
