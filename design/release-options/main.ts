import { createApp } from 'vue'
import ReleaseOptionsPreview from '../../src/components/previews/ReleaseOptionsPreview.vue'
import '../../src/styles.css'

// Deliberately isolated: no app store, API client, profile writes, or release actions.
createApp(ReleaseOptionsPreview).mount('#app')
