# clear-dream

Clear Dream是一款Agent开发toolkit，封装了几个常用的组件，包括但不限于

- **openai-client** OpenAI格式的LLM API客户端
- **ToolRegistry** 工具（tool）的注册中心
- **Model** OpenAIClient + ToolRegistry实现的模型端
- **Graph** （实现中）DAG图实现

总的来说，Model + Graph = Agent

设计上很大程度参考了LangGraph等框架。

## 项目状态

目前这个toolkit仍然在开发中，本质上是为了自用，当然你喜欢也可以参与完善。。不过前提是你真的看得上这个。

## 例子

- `example/` — 基于 Model + ToolRegistry 的交互式 REPL（需要 `OPENAI_*` 环境变量）
- `example/graph/` — LLM Node + ToolNode 的 DAG agent loop（默认离线 mock）

```bash
go run ./example        # 交互式 REPL
go run ./example/graph  # LLM + ToolNode agent loop
```

## 为什么我要做这个开源项目

因为我感觉市面上的Agent框架都太重了。

我是一名独立App开发者，我喜欢在保证维护体验的前提上，架构上能省则省，生产环境都是Docker（这个省不了）跑的单体go程序，前面放个caddy做蓝绿发布流量切换。

为了省资源，我甚至在生产环境上使用WAL模式的sqlite作为主数据库，这在那台一年99的阿里云上运行得非常好，除了跑SQL费劲。

现有的Agent框架看过文档之后，感觉对于我自己的需求来看，设计过于复杂了，我的App都很简单，用不着那么强大复杂的功能。

有的时候我真的仅仅只是需要接一个DeepSeek，绑两个tools，让用户能用人话去访问我的功能就好了。

我并不觉得我在做一个framework，我更想做的是一个toolkit，一个能够让我日后方便的把这些东西嵌入到，我那12m的、支撑线上业务的二进制文件中的方便的工具箱。

## License

MIT