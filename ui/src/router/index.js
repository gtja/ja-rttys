/* SPDX-License-Identifier: MIT */
/*
 * Author: Jianhui Zhao <zhaojh329@gmail.com>
 */

import { createWebHistory, createRouter } from 'vue-router'
import axios from 'axios'

import Login from '../views/Login.vue'
import Home from '../views/Home.vue'
import Rtty from '../views/Rtty.vue'
import Error from '../views/Error.vue'
import OAuth from '../views/OAuth.vue'
import McpAccess from '../views/McpAccess.vue'
import { loginDestination } from '../loginRedirect'

const routes = [
  { path: '/oauth/consent', component: OAuth },
  { path: '/mcp-access', component: McpAccess },
  {
    path: '/login',
    name: 'Login',
    component: Login
  },
  {
    path: '/',
    name: 'Home',
    component: Home
  },
  {
    path: '/rtty/:devid',
    name: 'Rtty',
    component: Rtty,
    props: true
  },
  {
    path: '/serial/:devid',
    name: 'Serial',
    component: Rtty,
    props: route => ({ devid: route.params.devid, mode: 'serial' })
  },
  {
    path: '/error/:err',
    name: 'Error',
    component: Error,
    props: true
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

router.beforeEach((to, from, next) => {
  if (to.path !== '/login') {
    axios.get('/api/alive').then(() => {
      next()
    }).catch(() => {
      const redirect = loginDestination(to.fullPath)
      next({ name: 'Login', query: redirect === '/' ? {} : { redirect } })
    })
  } else {
    next()
  }
})

export default router
