import {createApp} from 'vue'
import App from './App.vue'
import './style.css';
import 'bootstrap-icons/font/bootstrap-icons.css';

function markBoot(name: string) {
  window?.go?.main?.App?.RecordBootMilestone?.(name)?.catch?.(() => {})
}

markBoot('main.ts:begin')
const app = createApp(App)
markBoot('main.ts:app-created')
app.mount('#app')
markBoot('main.ts:mount-complete')
requestAnimationFrame(() => {
  markBoot('main.ts:first-animation-frame')
})
