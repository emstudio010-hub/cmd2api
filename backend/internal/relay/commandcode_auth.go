package relay

import (
	"context"
	"errors"
	"net/url"
)

// commandCodeStudioBaseURL 是 Command Code 网站（他们内部叫 studio）的地址。
//
// 跟 cfg.BaseURL 那个 api.commandcode.ai 不是一回事：授权流走网站，不走 API。
// 官方 CLI 内部也把这两个地址分成两张表存，混用会拼出一个不存在的地址。
const commandCodeStudioBaseURL = "https://commandcode.ai"

// CommandCodeAuthURL 拼出浏览器授权页的地址。
//
// 这套流程是从官方 CLI 包里还原出来的（command-code@1.66.0 的
// buildCommandAuthUrl），不是文档里的东西：CLI 自己起一个 127.0.0.1 上的
// 临时服务收结果，浏览器跳到这个地址，用户登录完 studio 就把浏览器
// 303 回 callback，并在 query 里带上 apiKey / state / userId / userName /
// keyName。
//
// 关键结论：**这个流程的产物就是一把普通 API key**，跟手填进账号表的是
// 同一种东西。所以它对中转链路没有任何影响，只是「新建账号」那一步换了
// 个输入方式。
//
// callbackURL 必须是绝对地址，studio 拿到的是个完整 URL。
func CommandCodeAuthURL(callbackURL, state string) string {
	q := url.Values{}
	q.Set("callback", callbackURL)
	q.Set("state", state)
	// mode=redirect 才是「把浏览器跳回来」。不带这个参数时 studio 走的是
	// POST JSON 那条老路径，那条要配合 CORS 响应头，对浏览器直连更麻烦。
	q.Set("mode", "redirect")
	return commandCodeStudioBaseURL + "/studio/auth/cli?" + q.Encode()
}

// Whoami 是上游返回的账号身份。
type Whoami struct {
	// UserName 是站上的用户名，拿来做账号的默认名称很合适。
	UserName string
	// Name 是昵称，可能为空。
	Name string
}

// FetchWhoami 用一把密钥换账号身份，顺带判断这把密钥是否可用。
//
// 拿它而不是探活来验密钥，是因为它**不花钱**：whoami 不生成 token、不占额度，
// 而探活会真发一次生成请求。刚授权完就烧掉一次额度，观感很差。
//
// 上游 200 但不带 user 的情况按失败处理：那说明这把密钥没对应到任何账号，
// 让它蒙混过去就会建出一个永远用不了的账号。
func (c *Client) FetchWhoami(ctx context.Context, apiKey string) (Whoami, error) {
	var raw struct {
		Success bool `json:"success"`
		User    *struct {
			UserName string `json:"userName"`
			Name     string `json:"name"`
		} `json:"user"`
	}
	if err := c.getJSON(ctx, apiKey, "/alpha/whoami", &raw); err != nil {
		return Whoami{}, err
	}
	if raw.User == nil {
		return Whoami{}, errors.New("上游没有返回账号信息，密钥可能无效")
	}
	return Whoami{UserName: raw.User.UserName, Name: raw.User.Name}, nil
}
