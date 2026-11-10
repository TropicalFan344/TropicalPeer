import { mount } from 'svelte'
import './app.css'
import App from './App.svelte'
import { initTheme } from './lib/theme.svelte'
import { i18n, loadLanguage } from './lib/i18n.svelte'

initTheme()

// The language in use loads before the first paint, so nothing flashes in English.
loadLanguage(i18n.lang).finally(() => mount(App, { target: document.getElementById('app')! }))
