// localStorage 在隐私模式 / 禁用 Cookie 的环境下会直接抛错。
// 主题和令牌都不值得为它崩掉整个应用，所以统一包一层。

export function readStore(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

export function writeStore(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    /* 存不下就算了，本次会话内仍然可用 */
  }
}

export function removeStore(key: string): void {
  try {
    window.localStorage.removeItem(key)
  } catch {
    /* 同上 */
  }
}
