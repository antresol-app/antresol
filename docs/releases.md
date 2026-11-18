---
title: Releases
---

# Releases

What changed in each version of the server. Install or update: [Install](install.html). Every release with its files is also on [GitHub](https://github.com/antresol-app/antresol/releases).

{% for release in site.github.releases %}{% unless release.draft %}
<section class="release">
<h2 id="{{ release.tag_name }}"><a href="{{ release.html_url }}">{{ release.name | default: release.tag_name }}</a></h2>
<p class="date">{{ release.published_at | date: "%-d %B %Y" }}{% if release.prerelease %} · pre-release{% endif %}</p>
{{ release.body | markdownify }}
</section>
{% endunless %}{% endfor %}
