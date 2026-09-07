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
 * @FileName: stdin.go
 * @Description: [提问前丢掉 stdin 里积着的按键，避免长任务期间误按回车被当成菜单选择]
 */

package cli

import "github.com/AlecAivazis/survey/v2"

// ask 提问前先清掉误按的回车，再走 survey.Ask。
func ask(qs []*survey.Question, response interface{}, opts ...survey.AskOpt) error {
	drainStdin()
	return survey.Ask(qs, response, opts...)
}

// askOne 提问前先清掉误按的回车，再走 survey.AskOne。
func askOne(p survey.Prompt, response interface{}, opts ...survey.AskOpt) error {
	drainStdin()
	return survey.AskOne(p, response, opts...)
}
