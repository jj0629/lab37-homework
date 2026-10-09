import { createRouter, createWebHistory } from 'vue-router'
import OrdersView from './views/OrdersView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', redirect: '/orders' },
    { path: '/orders', name: 'orders', component: OrdersView, meta: { title: 'Orders' } },
    {
      path: '/orders/:id(\\d+)',
      name: 'order',
      component: () => import('./views/OrderDetailView.vue'),
      props: (r) => ({ id: Number(r.params.id) }),
      meta: { title: 'Order' },
    },
    { path: '/ingestion', name: 'ingestion', component: () => import('./views/IngestionView.vue'), meta: { title: 'Ingestion' } },
    { path: '/review', name: 'review', component: () => import('./views/ReviewView.vue'), meta: { title: 'Review queue' } },
    { path: '/:pathMatch(.*)*', redirect: '/orders' },
  ],
})

router.afterEach((to) => {
  document.title = `${(to.meta.title as string) ?? 'Orders'} · Order Ops`
})
