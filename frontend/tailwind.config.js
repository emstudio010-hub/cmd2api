/** @type {import('tailwindcss').Config} */
// 语义化颜色令牌。所有组件只引用这些名字，绝不出现裸十六进制色值——
// 这样浅色/深色两套主题只需要在 main.css 里各定义一次变量。
//
// 变量以 "R G B" 三段通道值存放，配合 `<alpha-value>` 让 bg-accent/10
// 这类透明度写法照常可用。
const token = (name) => `rgb(var(--c-${name}) / <alpha-value>)`

export default {
  darkMode: 'class',
  content: ['./index.html', './src/**/*.{vue,ts,tsx,js}'],
  theme: {
    extend: {
      colors: {
        // 背景层级：页面 → 面板 → 抬升面 → 悬浮层
        canvas: token('canvas'),
        surface: token('surface'),
        raised: token('raised'),
        overlay: token('overlay'),

        // 描边
        line: {
          DEFAULT: token('line'),
          strong: token('line-strong'),
        },

        // 文字层级
        fg: token('fg'),
        muted: token('muted'),
        subtle: token('subtle'),

        // 主色与状态色
        accent: {
          DEFAULT: token('accent'),
          fg: token('accent-fg'),
        },
        success: token('success'),
        warning: token('warning'),
        danger: token('danger'),
        info: token('info'),
      },
      borderColor: {
        DEFAULT: token('line'),
      },
      divideColor: {
        DEFAULT: token('line'),
      },
      ringColor: {
        DEFAULT: token('accent'),
      },
      fontFamily: {
        sans: [
          'Inter',
          '-apple-system',
          'BlinkMacSystemFont',
          'Segoe UI',
          'PingFang SC',
          'Hiragino Sans GB',
          'Microsoft YaHei',
          'Noto Sans SC',
          'Roboto',
          'Helvetica Neue',
          'Arial',
          'sans-serif',
        ],
        mono: [
          'JetBrains Mono',
          'SFMono-Regular',
          'Menlo',
          'Consolas',
          'Liberation Mono',
          'monospace',
        ],
      },
      fontSize: {
        '2xs': ['0.6875rem', { lineHeight: '1rem' }],
      },
      screens: {
        // 侧栏折叠的断点。用 900px 而不是 Tailwind 默认的 lg(1024)：
        // 平板横屏下 1024 会把内容区挤得太窄。
        nav: '900px',
      },
      borderRadius: {
        panel: '0.625rem',
      },
      boxShadow: {
        panel: '0 1px 2px 0 rgb(0 0 0 / 0.08)',
        pop: '0 16px 40px -12px rgb(0 0 0 / 0.45), 0 2px 8px -2px rgb(0 0 0 / 0.3)',
      },
      transitionDuration: {
        DEFAULT: '150ms',
      },
    },
  },
  plugins: [],
}
