# Changelog

## 0.1.0 (2026-05-16)


### Features

* add credential store ([774a5f7](https://github.com/fiam/toolmux/commit/774a5f7716b8eefa400b6dee9275d2cedb94d1d6))
* **auth:** add interactive progress UI ([5e0e042](https://github.com/fiam/toolmux/commit/5e0e042661572b04b7157017a19177eb42a173df))
* **cli:** add status doctor plumbing ([4030939](https://github.com/fiam/toolmux/commit/4030939e1f762d2ca4ec5d203c5e10e383542dff))
* **cli:** add top-level toolbox registration ([9fb89bc](https://github.com/fiam/toolmux/commit/9fb89bc0fc450382524f92cdec27cce9f324adf2))
* **linear:** prepare integration ([df0a2ed](https://github.com/fiam/toolmux/commit/df0a2eda1d390215a1747bfd3d4a1b4c5e9dc987))
* **mcp:** add agent tool server ([43e49dc](https://github.com/fiam/toolmux/commit/43e49dc4607cd526e39450054573959c36d60c9c))
* **mcp:** add command-backed stdio servers ([d661753](https://github.com/fiam/toolmux/commit/d6617534643d6392cc65666fe29998c8a7a21d9c))
* **mcp:** add remote defaults and auth metadata ([a633e62](https://github.com/fiam/toolmux/commit/a633e625bf060d39167926d18f9275b4e1a37c0e))
* **mcp:** add remote OAuth auth ([5ec41b6](https://github.com/fiam/toolmux/commit/5ec41b617c4f3da38246f40e25d6093d44007fe2))
* **mcp:** add remote server imports ([62ac097](https://github.com/fiam/toolmux/commit/62ac0976e8b42ca7524584b91cd8a48b21cb3f9a))
* **mcp:** configure tool call timeout ([e1d1203](https://github.com/fiam/toolmux/commit/e1d12031fb876c9d991c0dfee7a3df8353018ffc))
* **mcp:** embed remote catalog yaml ([8c019af](https://github.com/fiam/toolmux/commit/8c019af9aa3303d8d8f89d3392b5a53dfa4e0bc4))
* **mcp:** expand remote catalog ([737f018](https://github.com/fiam/toolmux/commit/737f018f76bb90f52bc685599e2237fe90d8a8ff))
* **mcp:** improve agent configuration ([4206bcc](https://github.com/fiam/toolmux/commit/4206bcc15b73912fbb392425670625f0027e204d))
* **mcp:** improve remote client compatibility ([87b2cbd](https://github.com/fiam/toolmux/commit/87b2cbdd55f6154991bb37eef0127b7ec142d5b4))
* **mcp:** move schema command under mcp ([054e42d](https://github.com/fiam/toolmux/commit/054e42daa43144e032d5dbbe1fa5f889d9fc3f7c))
* **mcp:** polish OAuth callback page ([f0276c9](https://github.com/fiam/toolmux/commit/f0276c9bb9bc536dcec3d98743e1a605cd1966e8))
* **notion:** add initial page integration ([31a2687](https://github.com/fiam/toolmux/commit/31a2687339263676f7735242e25cd066b6365688))
* **notion:** expand page and data source commands ([bd27154](https://github.com/fiam/toolmux/commit/bd27154a5e1822ce8ef1e4db419710e61f98f3b2))
* **server:** expose build info ([3eb5e67](https://github.com/fiam/toolmux/commit/3eb5e6719633b0078bb913acf3d8b04b3bff36d3))
* **slack:** add browser-session auth ([225fc9e](https://github.com/fiam/toolmux/commit/225fc9e6d53bb2756235285a9b1bb6bf72868ee5))
* **slack:** add experimental conversation listing ([89e420c](https://github.com/fiam/toolmux/commit/89e420c07be2810907b8357205244837a75abd00))
* **slack:** add initial provider commands ([2ab60ec](https://github.com/fiam/toolmux/commit/2ab60ec303ffffd7dca43d2a9a9a9cc10abc6a2c))
* **slack:** add native Slack toolbox ([7bd763a](https://github.com/fiam/toolmux/commit/7bd763af916842d76f47eda7624d2695d433e92c))
* **slack:** expose identity and time bounds ([3e42573](https://github.com/fiam/toolmux/commit/3e425732dbf0037b965c7903851d83d699eb4460))
* **workflow:** add Slack recap workflows ([1524fc9](https://github.com/fiam/toolmux/commit/1524fc96659f9e2dbf3acfc910467a7565bc1260))


### Bug Fixes

* **cli:** satisfy lint after toolbox cleanup ([ac08543](https://github.com/fiam/toolmux/commit/ac08543054d567ef8f5e1a1fca4d51c9d6184ca9))
* **mcp:** clear auth when removing remotes ([381327c](https://github.com/fiam/toolmux/commit/381327c5f3179f6a9172feda765e181143514b7f))
* **mcp:** improve remote help usability ([36b5571](https://github.com/fiam/toolmux/commit/36b5571f64e8c05ddfdb44709dd5e3fcb6a6c16e))
* **mcp:** read SSE responses until idle ([c134951](https://github.com/fiam/toolmux/commit/c13495159295a0b0b65e593a9cd77897a5ee3286))
* **mcp:** reject missing remote tools ([0db5a47](https://github.com/fiam/toolmux/commit/0db5a47df85a740a693a69d07fc16d503d673164))
* **mcp:** show remote help without a tool ([031ce61](https://github.com/fiam/toolmux/commit/031ce6174b52e758d1a0dd165f7fb0952cf8624e))
* **mcp:** skip SSE notifications before responses ([487f9f6](https://github.com/fiam/toolmux/commit/487f9f6fb3c09f5a2980c22f3f840bca55eee21d))
* **status:** show registered native toolboxes ([743db4d](https://github.com/fiam/toolmux/commit/743db4dbedef7c500955499029546e119df016ab))
