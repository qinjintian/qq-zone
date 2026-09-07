/*
 * Copyright (c) 2026 qinjintian. All rights reserved.
 *
 * No Part of this file may be reproduced, stored
 * in a retrieval system, or transmitted, in any form, or by any means,
 * electronic, mechanical, photocopying, recording, or otherwise,
 * without the prior consent of qinjintian.
 *
 * @Author: qinjintian<514092640@qq.com>
 * @Date: 2026-09-07
 * @FileName: progress.go
 * @Description: [网络等待时转圈，避免还没拉到进度数据时终端像死机]
 */

package app

import (
	"context"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// waitSpinner 在还不知道总数时转圈并显示已等待时间。
func waitSpinner(p *mpb.Progress, name string) *mpb.Bar {
	if p == nil {
		return nil
	}
	return p.New(-1,
		mpb.SpinnerStyle().PositionLeft(),
		mpb.BarRemoveOnComplete(),
		mpb.BarWidth(2),
		mpb.PrependDecorators(
			decor.Name(name+" ", decor.WC{W: 22, C: decor.DindentRight}),
		),
		mpb.AppendDecorators(
			decor.Name("已等待 "),
			decor.Elapsed(decor.ET_STYLE_MMSS),
		),
	)
}

// stopWaitSpinner 结束转圈并从表里拿掉，后面可以接着画真正的进度条。
func stopWaitSpinner(bar *mpb.Bar) {
	if bar == nil {
		return
	}
	bar.SetTotal(-1, true)
}

// WithWaitSpinner 单独包一次没有进度条的网络请求，例如拉相册列表。
func WithWaitSpinner(ctx context.Context, name string, fn func() error) error {
	p := mpb.NewWithContext(ctx)
	bar := waitSpinner(p, name)
	err := fn()
	stopWaitSpinner(bar)
	p.Wait()
	return err
}
