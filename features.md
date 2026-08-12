---
layout: default
title: Feature index
---
<p class="eyebrow">SHIPPED CAPABILITIES</p><h1>Feature index</h1><p class="lede">Every item below comes from <a href="https://github.com/Veyal/interseptor/blob/main/docs/FEATURES.md"><code>docs/FEATURES.md</code></a>, current repository source. This page adds navigation without copying its canonical prose.</p><div class="feature-list">{% for feature in site.data.features %}<article id="{{ feature.id }}"><span class="feature-kicker">{{ feature.number }}</span><h2>{{ feature.title }}</h2><p>{{ feature.text }}</p>{% if feature.link %}<a href="{{ feature.link | relative_url }}">Read operational guide →</a>{% endif %}</article>{% endfor %}</div>
