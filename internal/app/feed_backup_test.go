package app

import (
	"io"
	"testing"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

func TestFinishBarUnblocksWait(t *testing.T) {
	p := mpb.New(mpb.WithWidth(40), mpb.WithOutput(io.Discard))
	bar := p.AddBar(5,
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(decor.CountersNoUnit("%d / %d")),
	)
	bar.Increment()
	bar.Increment()
	finishBar(bar)

	done := make(chan struct{})
	go func() {
		p.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("进度条没走满时 Wait 没有返回")
	}
}
