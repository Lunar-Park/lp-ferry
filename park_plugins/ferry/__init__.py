"""Ferry, the ingest plugin: card offloads, proxies and Resolve projects as park jobs.

The package itself imports nothing, so the standard-library modules beside it
(``config``, ``probe``, ``layout``) load in a plain interpreter. The pieces park
loads, ``records`` and ``ops``, import park at their own layer.
"""
