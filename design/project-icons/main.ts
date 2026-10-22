import { createApp } from 'vue'
import Gallery from './Gallery.vue'
import '../../src/styles.css'

// Deliberately independent: no app stores, HTTP client, native IPC or persistence.
createApp(Gallery).mount('#app')
