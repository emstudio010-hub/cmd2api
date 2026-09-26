package relay

import (
	"context"
	"errors"
	"net/url"
	"strings"
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
func CommandCodeAuthURL(callbackURL, state string, redirectMode bool) string {
	q := url.Values{}
	q.Set("callback", callbackURL)
	q.Set("state", state)
	// mode=redirect 才是「把浏览器跳回来」：studio 拿它决定把结果当成一次
	// 顶层表单提交打进 callback，还是改用 fetch 发 JSON。
	//
	// 两种都留着，因为它们的失败姿势不一样，而我们要的正是那个失败姿势：
	//
	//   - 带 mode=redirect：POST 打不通就是浏览器停在一个打不开的页面上，
	//     没有任何救回来的余地。回调地址真有人接时才用。
	//   - 不带：fetch 打不通时 studio 会把密钥显示在 /studio/auth/cli/fallback
	//     上让用户复制。面板在远程、我们收不到那一下时，这条路能把密钥交到
	//     用户手里——链路从「跳回来」变成「抄回来」，其余一步不少。
	if redirectMode {
		q.Set("mode", "redirect")
	}
	return commandCodeStudioBaseURL + "/studio/auth/cli?" + q.Encode()
}

// CommandCodeCallbackResult 是从一段「回调结果」里解析出来的东西。
type CommandCodeCallbackResult struct {
	// APIKey 是 studio 带回来的上游密钥，也就是整趟授权要拿的东西。
	APIKey string
	// State 是发起时我们自己塞进去的那个，用来认回是哪一次握手。
	State string
	// Error / ErrorDescription 是用户在站上拒绝，或 studio 报错时回来的东西。
	Error            string
	ErrorDescription string
}

// ParseCommandCodeCallback 从用户粘回来的一段东西里取出授权结果。
//
// 为什么需要「粘贴」这条路：回调地址是 studio 让浏览器跳回**跑着 cmd2api
// 的那台机器**的地址。面板跑在远程服务器上、浏览器连不回来的时候，这一跳
// 会直接失败，页面停在一个打不开的地址上——而密钥就明明白白地在那个地址的
// query 里。让用户把地址栏里那条 URL 复制回来，比让他去翻开发者工具从网络
// 请求里找 apiKey 现实得多。
//
// 接受三种输入：
//
//   - 完整的回调地址（`https://panel.example.com/api/accounts/oauth/...?apiKey=...`）
//   - 没带 scheme 的地址（`127.0.0.1:8080/api/accounts/oauth/...?apiKey=...`）
//     ——浏览器地址栏里复制出来常是这个样子
//   - 一把裸密钥（`user_...`）
//
// 所以这里**不去 url.Parse 整串**：第二种情况下 url.Parse 会把
// 「127.0.0.1:8080」当成 scheme 解析，host 变成空的，query 也就丢了。只在
// 第一个 '?' 处切开、只解析后半段，上面三种就都能对——因为 studio 的
// encodeURIComponent 编码过 query，这里解码正是想要的。
func ParseCommandCodeCallback(raw string) (CommandCodeCallbackResult, error) {
	raw = strings.TrimSpace(raw)
	// 从终端或聊天窗口里复制常带上一整行甚至几行，只取第一行。
	if idx := strings.IndexAny(raw, "\r\n"); idx >= 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	if raw == "" {
		return CommandCodeCallbackResult{}, errors.New("请粘贴回调地址或密钥")
	}

	idx := strings.IndexByte(raw, '?')
	if idx < 0 {
		// 没有 query。带路径或者带 scheme 的，那是把别的地址粘进来了——
		// 报错要指向「该粘哪一条」，不然用户只会反复粘同一个错的东西。
		if strings.Contains(raw, "://") || strings.Contains(raw, "/") {
			return CommandCodeCallbackResult{}, errors.New(
				"这段地址里没有 apiKey。要粘的是浏览器跳转到、但打不开的那个地址栏里的完整 URL，" +
					"不是授权页地址，也不是面板地址")
		}
		// 裸密钥。这里不能顺手 Trim 掉引号，密钥里本来就可能什么都有。
		return CommandCodeCallbackResult{APIKey: raw}, nil
	}

	query := raw[idx+1:]
	// 有些地址带 fragment，它不属于 query。
	if hash := strings.IndexByte(query, '#'); hash >= 0 {
		query = query[:hash]
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return CommandCodeCallbackResult{}, errors.New("这段地址的查询参数解析不了，检查一下是不是没复制完整")
	}

	result := CommandCodeCallbackResult{
		APIKey:           strings.TrimSpace(values.Get("apiKey")),
		State:            strings.TrimSpace(values.Get("state")),
		Error:            strings.TrimSpace(values.Get("error")),
		ErrorDescription: strings.TrimSpace(values.Get("error_description")),
	}
	if result.APIKey == "" && result.Error == "" {
		// 最像的一种粘错：把**授权页**地址粘回来了。它的 query 里有 callback
		// 和 state，看着很像，但没有 apiKey——用户会以为是我们这边坏了。
		if values.Get("callback") != "" {
			return CommandCodeCallbackResult{}, errors.New(
				"这是授权页的地址，不是跳转后的地址。请先在那个页面完成登录，再把跳转后（打不开的）那条地址粘过来")
		}
		return CommandCodeCallbackResult{}, errors.New("这段地址里既没有 apiKey 也没有错误信息，不是回调地址")
	}
	return result, nil
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

// DescribeKeyCheckError 把一次密钥校验的失败转成给用户看的一句话。
//
// 放在这一层而不是 handler 里：要读懂上游的错误体（401 和 500 是两种意思、
// message 藏在哪一层）得知道上游的 wire 形状。handler 只负责把这句话显示
// 出去，不该也去解析一遍响应体。
//
// 返回的字符串可能为空——err 为 nil 时。调用方不该在这个前提下显示任何东西。
func DescribeKeyCheckError(err error) string {
	if err == nil {
		return ""
	}
	var statusErr *UpstreamStatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.IsAuthFailure():
			if statusErr.Message != "" {
				return "上游不认这把密钥（" + statusErr.Message + "）"
			}
			return "上游不认这把密钥，检查一下是不是复制错了或者已经失效"
		case statusErr.Status >= 500:
			// 上游自己出毛病。说清楚，别让用户去换一把好密钥。
			return "上游这会儿出了点问题，稍后再试"
		case statusErr.Message != "":
			return "上游拒绝了这次校验（" + statusErr.Message + "）"
		}
	}
	// 连不上、超时这一类。归到「没连上」而不是「密钥不对」——这两件事
	// 用户该做的动作完全不同。
	return "校验密钥时没能连上上游：" + truncate(err.Error(), 100)
}
