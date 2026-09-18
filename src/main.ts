import { createApp, watch } from 'vue'
import { createPinia } from 'pinia'
import App from './App.vue'
import './styles.css'
import './cardMotion.css'
import { initializeLocale, locale, tr } from './i18n'
import { getSystemLocale, setNativeLanguage } from './tauri/window'

async function bootstrap() {
  await initializeLocale(getSystemLocale)
  watch(locale, (language) => {
    document.documentElement.lang = language
    document.title = tr('RunDock 启动坞')
    void setNativeLanguage(language)
  }, { immediate: true })

  const app = createApp(App)
  app.use(createPinia())
  app.mount('#app')
}

void bootstrap()
