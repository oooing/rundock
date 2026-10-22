export const designs = [
  { id: 1, name: '标题图标', note: '当前方案', description: '图标紧邻项目名，识别直接，保持现有布局。', tradeoff: '最紧凑，也最熟悉。' },
  { id: 2, name: '右下水印', note: '你的想法', description: '大图标藏在右下角，渐变淡出，不抢文字。', tradeoff: '更安静，品牌感融入背景。' },
  { id: 3, name: '侧边浮影', note: '大幅裁切', description: '放大图标从右侧探入，向左淡出，融入卡片底色。', tradeoff: '构图更大胆，保留图标的原色。' },
  { id: 4, name: '居中淡印', note: '克制留白', description: '单色图标居中沉入背景，像纸面上的一枚淡印。', tradeoff: '最安静，文字和操作始终在前。' },
  { id: 5, name: '斜向暗纹', note: '细节质感', description: '小图标错落重复，形成向右渐显的轻柔底纹。', tradeoff: '更有细节，适合轮廓简洁的图标。' },
] as const

export const gripDesigns = [
  { id: 1, name: '顶部居中', note: '当前位置 · 推荐', description: '独立放在顶部中间，不占标题，也远离操作按钮。', tradeoff: '最均衡，拖拽区域一眼能找到。' },
  { id: 2, name: '图标左侧', note: '竖向三点', description: '把手放在 Logo 左边，与图标居中对齐。', tradeoff: '不占顶边；水印模式下位于项目名称左侧。' },
] as const

export const samples = [
  { id: 'localplay', name: 'localPlay', icon: 'localplay.png', color: '#1e293b', ports: [9100, 8009], group: '未分组' },
  { id: 'rundock', name: 'RunDock', icon: 'rundock.png', color: '#312e81', ports: [17656, 17655], group: '开发工具' },
  { id: 'duck', name: '看剧鸭', icon: 'duck.png', color: '#164e45', ports: [11784, 3000], group: '未分组' },
  { id: 'brickmuse', name: 'BrickMuse · 粒想工坊', icon: 'brickmuse.svg', color: '#78350f', ports: [4310, 4311], group: '创作工具' },
  { id: 'photography', name: 'VirtualPhotography', icon: 'photography.svg', color: '#1e293b', ports: [3000, 8000], group: '创作工具' },
]
export type Sample = typeof samples[number]
export type PreviewState = 'running' | 'stopped' | 'failed'
export type Surface = 'project' | 'neutral' | 'light'
