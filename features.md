---
layout: default
title: Feature index
---
<p class="mono-label">Workspace reference</p>
<h1>All features</h1>
<p class="lede">Find the part of Interseptor you need. Each capability below links to its guide; the <a href="{{ '/workspace/' | relative_url }}">workspace tour</a> follows the app's menus.</p>
<div class="feature-list">{% for feature in site.data.features %}<article class="card-surface" id="{{ feature.id }}"><span class="feature-kicker">{{ feature.number }}</span><h2 id="{{ feature.id }}-heading">{{ feature.title }}</h2><p>{{ feature.text }}</p><a href="{{ feature.link | relative_url }}">Read guide <span aria-hidden="true">→</span></a></article>{% endfor %}</div>
