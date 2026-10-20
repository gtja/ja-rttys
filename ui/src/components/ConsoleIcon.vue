<template><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name] || paths.device"/></svg></template>
<script setup>
defineProps({name:String})
const paths={
 device:'M4 4h16v12H4z M8 20h8 M12 16v4 M8 8h.01 M11 8h5 M8 12h8',
 folder:'M3 7V5h7l2 2h9v13H3z',
 users:'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2 M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8 M22 21v-2a4 4 0 0 0-3-3.87 M16 3.13a4 4 0 0 1 0 7.75',
 history:'M3 12a9 9 0 1 0 3-6.7 M3 3v5h5 M12 7v5l3 2',
 settings:'M4 5h16 M4 12h16 M4 19h16 M8 3v4 M16 10v4 M10 17v4',
 plus:'M12 5v14 M5 12h14',
 search:'M21 21l-5-5 M10 17a7 7 0 1 0 0-14 7 7 0 0 0 0 14',
 arrow:'M9 5l7 7-7 7',
 terminal:'M4 6l6 6-6 6 M12 18h8',
 refresh:'M20 7a9 9 0 0 0-15-2L2 8 M2 3v5h5 M4 17a9 9 0 0 0 15 2l3-3 M17 16h5v5',
 more:'M5 12h.01 M12 12h.01 M19 12h.01',
 logout:'M9 5H4v14h5 M14 8l4 4-4 4 M8 12h10',
 key:'M15 9a4 4 0 1 0-8 0 4 4 0 0 0 8 0 M11 13v8 M11 17h3',
 close:'M6 6l12 12 M18 6L6 18',
 check:'M5 12l4 4L19 6'
}
</script>
