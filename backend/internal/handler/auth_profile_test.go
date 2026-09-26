package handler

import "testing"

// TestValidateEmail 钉住「登录用户名」这一关放行什么、挡住什么。
//
// 这个系统没有单独的 username 字段，登录身份就是 email，所以这里挡的其实
// 是「什么样的串能成为登录名」。挡松了会存进一个永远登不进去的名字——
// 而且下次启动 bootstrap 还会拿它去比对。
func TestValidateEmail(t *testing.T) {
	ok := []string{
		"admin@cmd2api.local",
		"a@b.co",
		"first.last+tag@sub.example.com",
		"用户@example.com", // 非 ASCII 本地部分：net/mail 认，就别自己拦
		// net/mail 接受不带点的域名（"a@b" 在 RFC 层面合法）。这里就跟着放行：
		// 这一关要挡的是「一串空格」「一个汉字」这种根本没法当登录名的东西，
		// 不是去替用户判断他的域名像不像域名。
		"a@b",
	}
	for _, email := range ok {
		if msg := validateEmail(email); msg != "" {
			t.Errorf("%q 应当通过，却被拒: %s", email, msg)
		}
	}

	bad := []struct {
		email string
		why   string
	}{
		{"", "空"},
		{"   ", "只有空白"},
		{"admin", "没有 @"},
		{"admin@", "没有域名"},
		{"@example.com", "没有本地部分"},
		{"admin @example.com", "中间有空格"},
		{"admin@example.com\nX-Injected: 1", "带换行（头部注入的经典形状）"},
		{"Admin <admin@example.com>", "带显示名——存进去会变成登录名的一部分"},
	}
	for _, tc := range bad {
		if msg := validateEmail(tc.email); msg == "" {
			t.Errorf("%q（%s）应当被拒，却通过了", tc.email, tc.why)
		}
	}
}

// TestValidateEmailRejectsTooLong 确认长度在写库之前就被挡住。
//
// user 表的 email 是 varchar(255)。放过去的话，用户看到的是数据库层的
// 约束错误，一句都读不懂。
func TestValidateEmailRejectsTooLong(t *testing.T) {
	long := ""
	for i := 0; i < maxEmailLen; i++ {
		long += "a"
	}
	long += "@example.com"

	if msg := validateEmail(long); msg == "" {
		t.Fatal("超长邮箱应当被拒")
	}
	// 长度要按字节算得上界，光按字符数算不够——多字节地址一样会撑爆列宽。
	if msg := validateEmail("admin@cmd2api.local"); msg != "" {
		t.Errorf("正常邮箱被误伤了: %s", msg)
	}
}
