// 提问前丢掉 stdin 里积着的按键，避免长任务期间误按回车被当成菜单选择

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
