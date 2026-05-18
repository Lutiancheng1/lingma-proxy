<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import JsonTree from './JsonTree.vue'

const props = defineProps({
  body: {
    type: String,
    default: '',
  },
  emptyText: {
    type: String,
    default: '空内容',
  },
})

const emit = defineEmits(['activated'])

const shellRef = ref(null)
const contentRef = ref(null)
const inputRef = ref(null)
const finderOpen = ref(false)
const searchQuery = ref('')
const matchCount = ref(0)
const activeMatchIndex = ref(-1)
let matchNodes = []

const parsed = computed(() => {
  const raw = String(props.body || '').trim()
  if (!raw) {
    return { valid: false, empty: true, text: props.emptyText }
  }
  try {
    return { valid: true, value: JSON.parse(raw) }
  } catch (e) {
    return { valid: false, empty: false, text: props.body }
  }
})

function activateViewer() {
  emit('activated')
}

function clearHighlights() {
  matchNodes.forEach((mark) => {
    const parent = mark.parentNode
    if (!parent) return
    const text = document.createTextNode(mark.textContent || '')
    parent.replaceChild(text, mark)
    parent.normalize()
  })
  matchNodes = []
  matchCount.value = 0
  activeMatchIndex.value = -1
}

function collectTextNodes() {
  if (!contentRef.value) return []
  const walker = document.createTreeWalker(
    contentRef.value,
    NodeFilter.SHOW_TEXT,
    {
      acceptNode(node) {
        if (!node.nodeValue || !node.nodeValue.trim()) {
          return NodeFilter.FILTER_REJECT
        }
        const parent = node.parentElement
        if (!parent || parent.closest('.viewer-finder')) {
          return NodeFilter.FILTER_REJECT
        }
        if (parent.tagName === 'MARK') {
          return NodeFilter.FILTER_REJECT
        }
        return NodeFilter.FILTER_ACCEPT
      }
    }
  )
  const nodes = []
  while (walker.nextNode()) {
    nodes.push(walker.currentNode)
  }
  return nodes
}

function updateActiveMatch(scroll = true) {
  matchNodes.forEach((mark, index) => {
    mark.classList.toggle('active', index === activeMatchIndex.value)
  })
  if (!scroll) return
  const active = matchNodes[activeMatchIndex.value]
  active?.scrollIntoView({ block: 'center', inline: 'nearest' })
}

function applyHighlights() {
  clearHighlights()
  const term = searchQuery.value.trim()
  if (!term || !contentRef.value) return

  const lowerTerm = term.toLowerCase()
  const textNodes = collectTextNodes()
  textNodes.forEach((textNode) => {
    let currentNode = textNode
    while (currentNode && currentNode.nodeValue) {
      const index = currentNode.nodeValue.toLowerCase().indexOf(lowerTerm)
      if (index === -1) break
      const matchNode = currentNode.splitText(index)
      currentNode = matchNode.splitText(term.length)
      const mark = document.createElement('mark')
      mark.className = 'viewer-search-match'
      mark.textContent = matchNode.nodeValue
      matchNode.parentNode?.replaceChild(mark, matchNode)
      matchNodes.push(mark)
    }
  })

  matchCount.value = matchNodes.length
  if (matchNodes.length > 0) {
    activeMatchIndex.value = 0
    updateActiveMatch(false)
  }
}

function moveMatch(direction) {
  if (matchNodes.length === 0) return
  const total = matchNodes.length
  activeMatchIndex.value = (activeMatchIndex.value + direction + total) % total
  updateActiveMatch(true)
}

async function openFinder() {
  finderOpen.value = true
  activateViewer()
  await nextTick()
  inputRef.value?.focus()
  inputRef.value?.select()
  applyHighlights()
}

function closeFinder() {
  finderOpen.value = false
  searchQuery.value = ''
  clearHighlights()
  nextTick(() => {
    shellRef.value?.focus()
  })
}

async function toggleFinder() {
  if (finderOpen.value) {
    closeFinder()
    return
  }
  await openFinder()
}

function onFinderKeydown(event) {
  if (event.key === 'Enter') {
    event.preventDefault()
    moveMatch(event.shiftKey ? -1 : 1)
  } else if (event.key === 'Escape') {
    event.preventDefault()
    closeFinder()
  }
}

watch(searchQuery, async () => {
  await nextTick()
  applyHighlights()
})

watch(() => props.body, async () => {
  await nextTick()
  if (finderOpen.value && searchQuery.value.trim()) {
    applyHighlights()
  } else {
    clearHighlights()
  }
})

watch(parsed, async () => {
  await nextTick()
  if (finderOpen.value && searchQuery.value.trim()) {
    applyHighlights()
  }
}, { deep: true })

onBeforeUnmount(() => {
  clearHighlights()
})

defineExpose({
  toggleFinder,
  openFinder,
  closeFinder,
})
</script>

<template>
  <div
    ref="shellRef"
    class="json-viewer-shell"
    tabindex="0"
    @focusin="activateViewer"
    @mousedown="activateViewer"
  >
    <div v-if="finderOpen" class="viewer-finder" @mousedown.stop>
      <input
        ref="inputRef"
        v-model="searchQuery"
        class="viewer-finder-input"
        type="search"
        placeholder="搜索"
        @keydown="onFinderKeydown"
      />
      <span class="viewer-finder-count">{{ matchCount > 0 ? `${activeMatchIndex + 1}/${matchCount}` : '0/0' }}</span>
      <button type="button" class="viewer-finder-button" :disabled="matchCount === 0" @click="moveMatch(-1)">
        <i class="bi bi-chevron-up"></i>
      </button>
      <button type="button" class="viewer-finder-button" :disabled="matchCount === 0" @click="moveMatch(1)">
        <i class="bi bi-chevron-down"></i>
      </button>
      <button type="button" class="viewer-finder-button" @click="closeFinder">
        <i class="bi bi-x-lg"></i>
      </button>
    </div>
    <div ref="contentRef" class="json-viewer hidden-scrollbar" :class="{ empty: parsed.empty }">
      <JsonTree v-if="parsed.valid" :value="parsed.value" root />
      <pre v-else>{{ parsed.text }}</pre>
    </div>
  </div>
</template>
