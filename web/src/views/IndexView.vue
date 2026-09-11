<script setup lang="ts">
import AppHeader from "@/components/layout/AppHeader.vue";
import FileBrowser from "@/components/file/FileBrowser.vue";
</script>

<template>
  <div class="page">
    <AppHeader />
    <main class="page__main">
      <div class="container">
        <FileBrowser />
      </div>
    </main>
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  min-height: 100vh;
}
.page__main {
  flex: 1;
  background: var(--bg);
}
</style>
