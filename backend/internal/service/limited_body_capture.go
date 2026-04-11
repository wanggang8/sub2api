package service

import "bytes"

type limitedBodyCapture struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
	totalBytes int
}

func newLimitedBodyCapture(limit int) *limitedBodyCapture {
	return &limitedBodyCapture{limit: limit}
}

func (c *limitedBodyCapture) Write(data []byte) {
	if len(data) == 0 || c == nil || c.truncated {
		if c != nil {
			c.totalBytes += len(data)
		}
		return
	}
	c.totalBytes += len(data)
	remaining := c.limit - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return
	}
	if len(data) <= remaining {
		_, _ = c.buf.Write(data)
		return
	}
	_, _ = c.buf.Write(data[:remaining])
	c.truncated = true
}

func (c *limitedBodyCapture) Bytes() []byte {
	if c == nil {
		return nil
	}
	return c.buf.Bytes()
}

func (c *limitedBodyCapture) TotalBytes() int {
	if c == nil {
		return 0
	}
	return c.totalBytes
}

func (c *limitedBodyCapture) Truncated() bool {
	if c == nil {
		return false
	}
	return c.truncated
}
