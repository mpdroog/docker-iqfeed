package main

// github.com/valyala/bytebufferpool
// http://www.cockroachlabs.com/blog/how-to-optimize-garbage-collection-in-go/
// https://github.com/docker/docker/blob/master/pkg/pools/pools.go
import (
	"bufio"
	"io"
	"sync"
)

const buffer1M = 1024

var (
	// BufioReader1MPool is a pool which returns bufio.Reader with a 1M buffer.
	BufioReader1MPool *BufioReaderPool
	// BufioWriter1MPool is a pool which returns bufio.Writer with a 1M buffer.
	BufioWriter1MPool *BufioWriterPool
)

// BufioReaderPool is a bufio reader that uses sync.Pool.
type BufioReaderPool struct {
	pool sync.Pool
}

func init() {
	BufioReader1MPool = newBufioReaderPoolWithSize(buffer1M)
	BufioWriter1MPool = newBufioWriterPoolWithSize(buffer1M)
}

// newBufioReaderPoolWithSize is unexported because new pools should be
// added here to be shared where required.
func newBufioReaderPoolWithSize(size int) *BufioReaderPool {
	return &BufioReaderPool{
		pool: sync.Pool{
			New: func() interface{} { return bufio.NewReaderSize(nil, size) },
		},
	}
}

// Get returns a bufio.Reader which reads from r. The buffer size is that of the pool.
func (bufPool *BufioReaderPool) Get(r io.Reader) *bufio.Reader {
	if r == nil {
		panic("No reader given.")
	}
	buf := bufPool.pool.Get().(*bufio.Reader)
	buf.Reset(r)
	return buf
}

// Put puts the bufio.Reader back into the pool.
func (bufPool *BufioReaderPool) Put(b *bufio.Reader) {
	b.Reset(nil)
	bufPool.pool.Put(b)
}

// Copy is a convenience wrapper which uses a buffer to avoid allocation in io.Copy.
func Copy(dst io.Writer, src io.Reader) (written int64, err error) {
	buf := BufioReader1MPool.Get(src)
	written, err = io.Copy(dst, buf)
	BufioReader1MPool.Put(buf)
	return
}

// BufioWriterPool is a bufio writer that uses sync.Pool.
type BufioWriterPool struct {
	pool sync.Pool
}

// newBufioWriterPoolWithSize is unexported because new pools should be
// added here to be shared where required.
func newBufioWriterPoolWithSize(size int) *BufioWriterPool {
	return &BufioWriterPool{
		pool: sync.Pool{
			New: func() interface{} { return bufio.NewWriterSize(nil, size) },
		},
	}
}

// Get returns a bufio.Writer which writes to w. The buffer size is that of the pool.
func (bufPool *BufioWriterPool) Get(w io.Writer) *bufio.Writer {
	if w == nil {
		panic("No reader given.")
	}
	buf := bufPool.pool.Get().(*bufio.Writer)
	buf.Reset(w)
	return buf
}

// Put puts the bufio.Writer back into the pool.
func (bufPool *BufioWriterPool) Put(b *bufio.Writer) {
	b.Reset(nil)
	bufPool.pool.Put(b)
}
