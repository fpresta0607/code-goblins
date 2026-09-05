# Scout report: is there a market for a trading-visualization product for agents?

Prepared 2026-09-05.
Every price, count and quote below is attributed to the source I read it from, with the date I read it.
Anything I could not verify at the source is marked **unverified**.

---

## Verdict: do not build it. High confidence.

Do not build the split x-axis market profile / window-trader visualization.
Confidence: **high** on the "do not build" half, **medium** on the redirection half.
Three independent lines of evidence close it, and any one of them alone would be enough.

**One. The feature already ships, as a checkbox, in every incumbent you named, and none of them charge for it.**
Sierra Chart has had a right-click menu item literally called "Letters/Blocks In Own Column" since at least 2020-11-29, which is the date on the support thread where a user reported it missing and the engineering team replied "You need to update to the current version for those commands."
TradingView shipped "Split profile at this letter," "Merge with previous profile" and "Reset all merges and splits" in November 2025.
Quantower sells a page headed "Full or partial split," which reads verbatim: "Separate the profile into each column or divide it into two by the selected bar."
MotiveWave calls it "Align Letters."
ATAS calls it "Split Periods," and pairs it with "Highlight Opens / Highlight Closes," which is your per-column open and close.
The brief's definition of the product ("divides the profile into separate columns per period so each letter column shows its own high, low, open and close") is satisfied by name, today, in five products, in a price band starting at $26 a month.

**Two. The buyer does not ask for it, and I can rank exactly what they do ask for.**
ATAS runs a public, vote-counted feature board at `feedback.atas.net`.
ATAS is precisely the customer you would be selling to: order-flow futures traders paying roughly $85 a month for a profile-first platform.
Of the top twenty requests by votes, read 2026-09-05, **not one asks for a new profile layout**.
The top request is a Risk Management Module at 447 votes.
The four profile-adjacent requests in the top twenty are all incremental tweaks to existing profiles, and the most-voted of them sits at 158.
The one request that is closest to your product, "Market Profile Improvements (merging, splitting, more colours...)," has **24 votes** and has been "In Progress" for about three years.

**Three. The agent, the buyer in your pitch, does not consume images at all.**
`TauricResearch/TradingAgents` has **102,574 stars** and processes only numeric and text data.
`HKUDS/AI-Trader` has 22,200 stars and is text and API driven.
`LLMQuant/awesome-trading-agents` catalogues 30+ MCP servers and 40+ agent frameworks and lists **zero** visualization or charting tools for agents.
Meanwhile the largest open-source market profile library, `bfolkens/py-market-profile`, has **405 stars and has not been pushed to since 2023-10-30**, and the top three TPO-specific repositories on GitHub have 22, 22 and 13 stars.
The attention ratio between the leading LLM trading framework and the leading market profile library is **253 to 1**, in the wrong direction.

### The one thing in the brief I have to push back on

The brief instructs me to start from "the visual thesis is empirically dead as of 2026."
For the **product** claim, the brief is right and I have strengthened it: agent-to-broker infrastructure in 2026 is MCP servers returning JSON, an agent never needs a picture to get a price, and the largest agent frameworks confirm it by construction.

For the **scientific** claim, the brief overshoots, and the overshoot is the only part of this brief that might still be worth money.
Ma et al., "Agent Trading Arena" (arXiv:2502.17967, submitted 2025-02-25, revised 2025-09-02), built a virtual zero-sum stock market where LLM agents move prices, and found the exact opposite of the brief's premise, verbatim:

> "Experiments reveal that LLMs struggle with numerical reasoning when given plain-text data, often overfitting to local patterns and recent values. In contrast, chart-based visualizations significantly enhance both numerical reasoning and trading performance."

Their GPT-4o numbers: text-only 33.65% total return at Sharpe 2.142; visual input 35.76% at Sharpe 4.159; both together 47.69% at Sharpe 6.777.
And the very paper the brief cites as the tombstone, "Do VLMs Truly 'Read' Candlesticks?", contains the sentence "candlestick charts consistently outperform equivalent tabular data" in its own comparison against the XGBoost numeric baseline.
This does not resurrect the product. It does mean question 3 in the brief is genuinely open, and I treat it as the report's centre of gravity below.

---

## 1. Does anyone sell a split x-axis / windowed market profile, and does anyone want one?

**Sold today, by name, as an included feature. Not a gap.**

| Platform | The feature, quoted | Where | Price of the product it sits inside |
|---|---|---|---|
| Sierra Chart | "Letters/Blocks in Own Column"; "Letters/Blocks in Own Column - All Profiles"; also "Letters/Blocks in Own Profile and Standard Profile"; plus "Split Profile Here" | [TPO Profile Charts studies reference](https://www.sierrachart.com/index.php?page=doc%2FStudiesReference%2FTimePriceOpportunityCharts.html) | $26-56/mo (brief) |
| TradingView | "Split by blocks: Distributes blocks into each time block throughout the entire profile period"; right-click "Split profile at this letter" / "Merge with previous profile" / "Reset all merges and splits" | [TPO charts explained](https://www.tradingview.com/support/solutions/43000725590-time-price-opportunity-charts-explained/); shipped [Nov 2025](https://fxnewsgroup.com/forex-news/platforms/tradingview-enhances-tpo-charts-functionality/) | $12.95-199.95/mo |
| Quantower | "Full or partial split. Use splitting for a detailed analysis of profile information. Separate the profile into each column or divide it into two by the selected bar." | [TPO Profile chart product page](https://www.quantower.com/tpoprofile) | $70/mo, $49/mo annual |
| MotiveWave | "Align Letters" displays/splits each TPO letter into its own column | [Volume and Order Flow Analysis guide v6](https://www.motivewave.com/guides/MotiveWave_Volume_Analysis_Version_6.pdf) | not priced here |
| ATAS | "Sub Period defines the size of a TPO sub-period. You can also enable Split Periods, highlight sub-period opens and closes with Highlight Opens / Highlight Closes" | [Market profile & TPO help](https://help.atas.net/en/support/solutions/articles/72000602305-market-profile-tpo) | ~$85/mo (brief) |
| Bookmap | Session-level only: "Split Secondary Session" and "Split Tertiary Session" | [Market Profile addon KB](https://bookmap.com/knowledgebase/docs/Addons-Market-Profile) | $16-79/mo (brief) |

The Sierra Chart date is the killer detail.
The support thread ([ThreadID=58557](https://www.sierrachart.com/SupportBoard.php?ThreadID=58557), 2020-11-29) is a user complaining the split-column menu item was **missing**, and Sierra Chart replying that it exists and he needs to update.
That is a six-year-old shipped feature in a $26-a-month product.

Note the Quantower economics specifically, because they are the closest thing to a direct price test of your idea.
Quantower breaks its platform into add-on extensions and "TPO Profile Chart" is one of them, but the buy path on the TPO page routes to the $70/month all-in-one licence rather than a standalone price.
The extension prices render client-side and did not resolve to a fetch: **the standalone TPO add-on price is unverified**.
What is verified is that the split-column feature is used as a bullet point to sell a $70 platform, not as a product.

**Does anyone want one?** The forum record says: mildly, and they want merge more than split.
The ATAS request "Market Profile Improvements (merging, splitting, more colours...)" has 24 votes, status "In Progress," age about three years.
Separate ATAS requests exist for [cutting/splitting](https://feedback.atas.net/p/cut-market-profile) and [merging](https://feedback.atas.net/p/market-profile-merge-function).
There is an [MQL5 freelance job](https://www.mql5.com/en/job/113445) for a "TPO (market profile) indicator with Merge and Split functions" with a budget of **30 to 100 USD**.
That is the observed market-clearing price for someone to build this feature from scratch, once, bespoke.

---

## 2. The community demand signal, ranked by evidence of frequency

Reddit is hard-blocked to my crawler at the API, the domain filter, the PullPush archive and the two mirrors I probed (`safereddit.com` serves an Anubis proof-of-work wall, `redlib.catsarch.com` returns 403).
So I substituted three channels that produce **numbers** rather than anecdotes, which is a better answer to "rank by evidence of frequency" anyway.

### 2a. ATAS public feature board, top 20 by votes, read 2026-09-05

| Votes | Request |
|---|---|
| 447 | Risk Management Module |
| 430 | Continuous Futures Contracts with Back-Adjustment |
| 307 | Creation of connection with TRADOVATE |
| 281 | Multiple symbols on the same chart |
| 174 | Delta divergence indicator |
| 158 | Volume profile heatmap and draw peak and valley |
| 137 | Editable Indicators in ATAS (like Pinescript) |
| 133 | Improve Dynamic Level indicators for Market profile |
| 112 | TPO Singleprint visualization |
| 106 | Monthly Timeframe |
| 104 | Time zones with 30 or 45 minute offsets |
| 103 | Playback Controls for Market Replay |
| 93 | Support for various Linux distributions |
| 86 | Customizable Exit Strategies |
| 86 | Load only candles without clusters |
| 83 | Range charts not interrupting daily for Crypto |
| 79 | Local time zones incl. daylight savings |
| 68 | Market Profile & TPO objects for chart-range and rolling-time spans |
| 62 | MEXC Exchange Integration |
| 55 | Breakeven and Trailing for Multi-Level Strategy Settings |

Source: [feedback.atas.net](https://feedback.atas.net/), scraped 2026-09-05.
The board paginates client-side, so this is the top 20 and not the full corpus: **rankings below position 20 are unverified**.

**Read the shape of that list.**
The top four asks are risk management, correct historical data, broker connectivity and multi-instrument context.
Not one is a rendering.
The five recurring themes, in order of total votes:

1. **Risk and money management automation** (447 + 86 + 55 = 588). Position sizing, exit strategies, breakeven, trailing. This is the single largest cluster and it is the thing the brief's own ecosystem-gaps note calls "execution guardrails."
2. **Data correctness and continuity** (430 + 106 + 104 + 79 + 83 = 802 across five items). Back-adjusted continuous contracts, timezone handling, session boundaries. Boring, unglamorous, and the largest cluster by raw votes.
3. **Broker and exchange connectivity** (307 + 62). Tradovate, MEXC.
4. **Scriptability** (137). "Editable Indicators in ATAS (like Pinescript)."
5. **Profile refinements** (158 + 133 + 112 + 68 = 471). All four are refinements to existing profiles, none is a new profile geometry.

### 2b. TradingView community script popularity, by boosts, read 2026-09-05

Top scripts under the `marketprofile` tag:

| Boosts | Script |
|---|---|
| 4,549 | Volume Footprint: Measuring by Math & Geometry |
| 3,855 | FVG Retest Entry Engine |
| 850 | Order Flow Profiler (Zeiierman) |
| 626 | Footprint X-Ray [BOSWaves] |
| 481 | HTF Auction Candle (Zeiierman) |
| 367 | 3D Market Profile [BOSWaves] |
| 293 | Swing Gradient TPOs [BOSWaves] |
| 89 | Simple MA TPO |

Under `volumeprofile`, the top entries are 2,434 (Structural Liquidity & POC Matrix), 1,864 (HTF Log-Regression Volume Profile) and 1,149 (Value Area Reversals).

Two readings, both bad for you.
Within profile-land, **footprint beats TPO by roughly an order of magnitude** (4,549 versus 367).
And the second-place script under a market-profile tag is an FVG entry engine, which is not a profile at all, which tells you the tag is being farmed by the general ICT-retail audience rather than by market profile practitioners.

### 2c. GitHub, read via the API 2026-09-05

| Stars | Repo | Last push |
|---|---|---|
| 405 | `bfolkens/py-market-profile` | 2023-10-30 |
| 197 | `EarnForex/MarketProfile` (MT4/MT5/cTrader) | 2026-03-13 |
| 249 | `murtazayusuf/OrderflowChart` | 2024-09-27 |
| 82 | `srlcarlg/srl-ctrader-indicators` | 2026-08-19 |
| 22 | `cenobar/TPO` | 2021-08-23 |
| 22 | `beinghorizontal/tpo_btc` | 2021-01-07 |
| 13 | `sivamgr/tpo_market_profile` | 2019-06-26 |

Against, from the same API on the same day:

| Stars | Repo |
|---|---|
| 102,574 | `TauricResearch/TradingAgents` |
| 32,494 | `HKUDS/Vibe-Trading` |
| 31,615 | `hsliuping/TradingAgents-CN` |
| 22,200 | `HKUDS/AI-Trader` |
| 4,357 | `atilaahmettaner/tradingview-mcp` |
| 1,482 | `warproxxx/poly-maker` (Polymarket market maker) |
| 946 | `alpacahq/alpaca-mcp-server` |
| 667 | `caiovicentino/polymarket-mcp-server` |
| 444 | `LLMQuant/awesome-trading-agents` |
| 420 | `okx/agent-trade-kit` |

**The ranked list of what this community actually asks for, most to least frequent:**

1. Risk management and automated exit control that survives a prop firm's rules.
2. Clean, back-adjusted, timezone-correct historical futures data.
3. Broker and exchange connectivity, specifically Tradovate and the prop-firm-adjacent venues.
4. Scriptability inside the platform they already pay for.
5. Order flow and footprint refinements.
6. Profile geometry, distant last, and merge is wanted more than split.

---

## 3. Is there a machine-legibility angle? The decision-relevant question.

**Answer: reading (b) is a live, published hypothesis with named academic backing, and nobody has tested it on profile, footprint or heatmap renderings. That is a real gap. It is a gap in the literature, not a gap in a market.**

### The case that candlesticks are machine-hostile rather than vision being dead

Keith-Norambuena, "Toward a Machine Bertin: Why Visualization Needs Design Principles for Machine Cognition," [arXiv:2602.01527](https://arxiv.org/html/2602.01527), February 2026.
This paper is your thesis, published, four months before this brief was written.
Abstract, verbatim:

> "Visualization's design knowledge, effectiveness rankings, encoding guidelines, color models, preattentive processing rules, derives from six decades of psychophysical studies of human vision. Yet vision-language models (VLMs) increasingly consume chart images in automated analysis pipelines, and a growing body of benchmark evidence indicates that this human-centered knowledge base does not straightforwardly transfer to machine audiences."

And, verbatim:

> "derendering evidence demonstrates that charts designed according to human perceptual principles are obstacles for machines."

The evidence it assembles: CharXiv shows a 33-point human-model gap (humans 80.5%, GPT-4o 47.1%); ChartMuseum 93% human against 63% best model; GPT-4 at the 16th percentile on a human visualization literacy assessment.

Note carefully what this paper is and is not.
It is a **position paper calling for a research programme**. It does not demonstrate a machine-native encoding that outperforms. It says someone should go find one.

### What the encoding evidence says about a profile specifically

Mukherjee, Ren, Moritz and Assogba (Apple), "EncQA: Benchmarking Vision-Language Models on Visual Encodings for Charts," [arXiv:2508.04650](https://arxiv.org/abs/2508.04650), submitted 2025-08-06, published in IEEE TVCG January 2026, code at [apple/ml-encqa](https://github.com/apple/ml-encqa).
2,076 synthetic question-answer pairs across six encoding channels (position, length, area, colour quantitative, colour nominal, shape) and eight tasks, nine VLMs.

The finding that matters to you: accuracy is highest for **position aligned to axes**, where values can be read directly off gridlines; **moderate for length, nominal colour and shape**; lowest for **area and quantitative colour**.
And, verbatim from the abstract: "Contrary to expectations, we find that performance does not improve with model size for many task-encoding pairs."

Now apply that hierarchy to your product honestly.
A candlestick is position-non-aligned plus length.
A market profile is length from an aligned baseline, which is one rung up.
A footprint chart is a grid of small numbers, which is OCR, not encoding.
A liquidity heatmap is quantitative colour, which is the **worst** channel on EncQA.
So the encoding literature gives a weak prior that a profile reads slightly better than a candlestick, and a strong prior that a heatmap reads worse than both.
That is a real, testable, cheap prediction. It is also nowhere near a product.

### The evidence that a better rendering will not fix it

Tartaglini, Grant, Wurgaft, Potts and Fan, "Diagnosing Bottlenecks in Data Visualization Understanding by Vision-Language Models," [arXiv:2510.21740](https://arxiv.org/abs/2510.21740), submitted 2025-10-02.
Verbatim:

> "the correct coordinates can be successfully read out from the latent representations in the vision encoder, suggesting that the source of these errors lies in the vision-language handoff."

Read that against the machine-legibility thesis, because it is the strongest single counter.
The pixels are already legible. The vision encoder extracts the coordinates correctly.
The failure is downstream, in the handoff from vision encoder to language model.
A rendering change operates on the half of the pipeline that is already working.
They also found that "providing correct coordinates helps with tasks involving one or a small number of data points, [but] it generally worsens performance for tasks that require extracting statistical relationships," which is exactly the class of task a market profile is for.

### Has anyone tested VLMs on profile, footprint or heatmap renderings?

**No, and I verified this at the source of the one paper that would have.**
"Do VLMs Truly 'Read' Candlesticks?" ([arXiv:2604.12659](https://arxiv.org/html/2604.12659v1), Hu, Xiao, Xu, Tang, Liu) uses **only candlestick charts. No other chart formats are evaluated.**
It also does not run a clean same-model image-versus-numeric ablation: it compares VLMs on charts against a separate XGBoost on numeric features, which are different model classes.
Its stated future work is fine-tuning, not representation.
Searches across arXiv for volume profile, TPO, footprint and order-flow-heatmap VLM evaluation returned nothing. **Unverified negative**: absence of evidence in the channels I searched, not proof no such work exists.

### Is there a benchmark to enter?

Three, and they are cheap:

- **EncQA** ([apple/ml-encqa](https://github.com/apple/ml-encqa)), open, 2,076 items, is the right harness to add a `market-profile` encoding condition to. It is Apple-authored and IEEE VIS published, so a contribution there is legible.
- **Agent Trading Arena** ([github.com/wekjsdvnm/Agent-Trading-Arena](https://github.com/wekjsdvnm/Agent-Trading-Arena)), open, is where the pro-visual result came from and is the natural place to swap the candlestick renderer for a profile renderer and rerun.
- **Agent Market Arena** ("When Agents Trade: Live Multi-Market Trading Benchmark for LLM Agents," [arXiv:2510.11695](https://arxiv.org/abs/2510.11695), Qian, Peng, Wang et al., submitted 2025-10-13, revised 2025-10-30), described as "the first lifelong, real-time benchmark for evaluating LLM-based trading agents across multiple markets," four agent architectures over five model backbones. Its headline finding is that "agent frameworks display markedly distinct behavioral patterns" while "model backbones contribute less to outcome variation," which is another way of saying the input pipeline matters more than the model. **No public leaderboard or submission URL found: unverified.**

**The honest summary of question 3.**
Reading (b) is not crazy. It has a February 2026 position paper arguing it, an encoding benchmark whose hierarchy weakly favours a profile over a candlestick, and a simulation result where charts beat plain text badly.
It also has a mechanistic result saying the bottleneck is not where a renderer can reach, an untested gap where profile and footprint renderings should be, and zero commercial pull anywhere in the agent ecosystem.
The correct response to a live hypothesis with no market is a cheap experiment, not a product. See the ranked list at the end.

---

## 4. The most demanding market for agents right now, ranked

Ranked by money actually moving, cross-checked against unmet need. Prices and volumes are read 2026-09-05 unless dated otherwise.

| Rank | Market | Money moving | Unmet need | Verdict |
|---|---|---|---|---|
| 1 | **Crypto perpetuals** | Binance alone ~$69B/day; Hyperliquid ~$432B/month (June 2026); perp DEXs processed ~$2.41T Jan-Mar 2026 | Native, uncapped monetisation rail for third-party interfaces | Highest. And it is the only venue that pays builders per trade. |
| 2 | **Prediction markets, specifically Kalshi** | Kalshi $14.2B trailing 30-day, +0.2%; Polymarket $3.0B, **-52.4%**; Kalshi 82-84% share | Institutional-grade market data and execution, actively subsidised | Second. But the venue is building the missing piece itself. |
| 3 | **Futures prop evaluation** | $850M industry revenue 2026, +45% YoY; 2.1M funded traders; 12M annual challenge signups | Risk management automation that survives funded-account rules | Largest retail buyer population, smallest wallet, and the operator captures the value, not the tool vendor. |
| 4 | **Compliance and audit for agents** | Not sized here | SEBI's algo framework binding since 2026-04-01 | The only lane with a legal forcing function. Underrated. |
| 5 | **Options flow** | Unusual Whales "50k+ Active Traders," "2M+ Options Tracked Daily" | Saturated. Three vendors at three price points. | Mature, defended, no agent angle. |
| 6 | **Equities research / execution / TCA** | Institutional, opaque | Held by incumbents | Not reachable from here. |

### #1 crypto perps: who pays, how much, what they buy

They do not pay a subscription. They pay a **fee share per routed trade**, which is a fundamentally better business than $25 a month.

Hyperliquid builder codes, per [Dwellir's writeup](https://www.dwellir.com/blog/hyperliquid-builder-codes) (**publication date not stated on the page: unverified**), verbatim:

> "$40 million in builder code revenue has flowed to developers since launch, with 40% of Hyperliquid's daily active users now trading through third-party frontends."

Caps, verbatim: "Perpetual futures maximum 0.1% (10 basis points); Spot trading maximum 1% (100 basis points)."
Minimum to become a builder: 100 USDC in the perpetuals account.
Named examples: Phantom wallet at approximately $100,000 per day, PVP.trade at "$7.2 million in lifetime revenue."
Corroborated on the fee-share mechanics by [Blockworks' builder-fee dashboard](https://blockworks.com/analytics/hyperliquid/hyperliquid-builder-codes/hyperliquid-builder-codes-trading-builder-fees) and [BloFin](https://blofin.com/en/academy/education/hyperliquid/hyperliquid-builder-codes-explained). The per-app dollar figures are from a vendor blog and are **unverified against on-chain data**.

**What they buy today:** third-party frontends and agent surfaces that route order flow. Not charts. The 40% figure is the demand signal: nearly half the venue's users already prefer someone else's interface to the exchange's own.

Caveat: perp DEX volumes have fallen over 50% since October per [ForkLog](https://forklog.com/en/perp-dex-trading-volumes-plummet-over-50-since-october/), and a more recent figure puts Hyperliquid at $185.5B trailing month against the $432B June peak. The trend is down.

### #2 Kalshi: who pays, how much, what they buy

Systematic trading firms and market makers, buying **latency and depth**, and Kalshi is paying them to show up.

On 2026-08-12, Kalshi launched a machine-readable real-time order book feed with Level 1 and Level 2 depth for sports contracts and crypto perpetual futures, which are "nearly 60% of Kalshi's weekly notional volume," built with low-latency provider DoubleZero Edge and replacing "previous REST API workarounds that added latency" ([Finance Magnates](https://www.financemagnates.com/fintech/kalshi-launches-real-time-level-2-data-feed-for-institutional-traders/), 2026-08-12).
Andy Roth, head of institutional business, verbatim: "Better connectivity makes better markets. Firms currently active on Kalshi are demanding institutional-grade infrastructure across all trading environments."
Pricing: on request. **Kalshi is waiving its share of institutional data revenue for the first year** to pull systematic firms in.

Read the strategic meaning of that.
A venue that waives its data revenue to recruit bots is a venue that has diagnosed its own bottleneck as bot participation, and is filling it itself, with fibre.
The gap you would fill on Kalshi is being closed by Kalshi, for free, right now.

The retail tooling layer above it is already priced and already crowded: Polytraders founder tier at $24.50/month locked for life (pre-launch, Q1 2027), a Kalshi bot at $99/month, Turbine Pro at $199/month, against a free Kalshi API and a free Polymarket SDK on a $5-20 VPS.

Also note Polymarket's trajectory: **-52.4% month over month**, 16-18% share against Kalshi's 82-84%. The brief's "14 of the 20 most profitable Polymarket wallets are bots" is a fact about a market that is shrinking fast in relative terms.

### #3 prop firms: the number in the brief needs a footnote

The brief states the industry is "valued at $20B across 2,000+ firms."
The best-sourced 2026 estimate I found says something 23 times smaller: **$850 million in 2026 revenue, up 45% year-over-year**, from "30+ active retail prop trading firms," with the top five controlling 62% ($530M), split as 42% challenge subscription fees, 38% trader profit-split, 15% affiliate commissions and **5% miscellaneous including whitelabel, API and tools** ([track360, 2026-05-14](https://track360.io/blog/prop-trading-industry-report-2026-market-analysis)).
Their methodology is explicitly estimation: "trader-base leaks (LinkedIn headcount, affiliate network data, challenge-signup scraped from ads), social media mentions, and regulatory filings." **Treat as approximate.**
Named revenue: FTMO $180-210M, FundedNext $110-140M, The 5%ers $65-85M, Apex $50-70M, TopStep $45-60M.

Two consequences.
First, the entire addressable tools slice of this industry is 5% of $850M, which is roughly **$42 million a year across every tool vendor combined**. Reconcile that with the brief's $20B before sizing anything.
Second, the automation rules moved in your favour and nobody has updated the received wisdom.
Apex still permits automation during evaluation and bans it on funded Performance Accounts.
**Topstep now offers API access for bots through its ProjectX-based TopstepX platform and permits automated strategies in both the Trading Combine and Funded Accounts**, subject to consistency and risk rules and a ban on HFT and latency arbitrage (secondary sources, [ClearEdge 2026-05-07](https://clearedge.trading/post/topstep-vs-apex-automated-trading-rules-bot-comparison) and [pickmytrade](https://pickmytrade.io/faq/prop-firm-automation); `topstep.com/rules/` returned HTTP 404 on 2026-09-05, so **the primary rule text is unverified**).

### #4 compliance: the only lane with a law behind it

India's SEBI algo framework became binding on all brokers on **2026-04-01**.
Every algorithmic order must carry an exchange-assigned **Algo-ID**. The broker is the principal and any algo provider operating through the broker's API is the broker's agent, making the broker legally liable for every algo order on its platform. Whitelisted static IPs, OAuth, 2FA, and mandatory session logout before pre-open. Black-box algos require the provider to hold a SEBI Research Analyst licence and publish periodic performance and risk disclosures. Brokers who missed the milestones were barred from onboarding new retail API clients from 2026-01-05.

That maps one-to-one onto three of the four gaps in `awesome-trading-agents`: execution guardrails, regulatory reporting and audit trails, and live performance transparency.
It is the only demand signal in this entire report that is backed by a regulator rather than by a vote count.
It is also India-specific and I have not checked whether equivalent rules are landing elsewhere: **scope unverified**.

---

## 5. Willingness to pay, evidenced

Every price I verified, with what it buys.

| Product | Price | Scale, where discoverable | Source |
|---|---|---|---|
| TradingView Essential | $12.95/mo annual | | [pricing](https://www.tradingview.com/pricing/), read 2026-09-05 |
| TradingView Plus | $29.95/mo annual | | same |
| TradingView Premium | $59.95/mo annual | | same |
| TradingView Ultimate | $199.95/mo annual | | same |
| Quantower All-in-One | $70/mo; $63 3mo; $56 6mo; **$49/mo annual** | | [pricing](https://www.quantower.com/pricing) |
| Quantower TPO add-on standalone | **unverified**, renders client-side | | [tpoprofile](https://www.quantower.com/tpoprofile) |
| Sierra Chart | $26-56/mo | | brief |
| Bookmap | $16-79/mo; $990 / $1,990 lifetime | **$10.3M revenue (2023)**, ~55 staff 2026, bootstrapped, no funding reported | brief; [getlatka](https://getlatka.com/companies/bookmap.com) |
| ATAS | ~$85/mo | | brief |
| NinjaTrader | $99/mo | | brief |
| Tradovate | free tier; Active Trader ~$99/mo; Lifetime ~$1,499 | | secondary, **unverified** |
| TopstepX platform | $0/mo | | secondary, **unverified** |
| Topstep Combine | $49 (50K) / $99 (100K) / $149 (150K) per month | | secondary, **unverified** |
| Unusual Whales Retail Basic | $50/mo, $42/mo annual | **"50k+ Active Traders," "2M+ Options Tracked Daily"** | [unusualwhales.com](https://unusualwhales.com/lp/best-options-flow-tracker) |
| Unusual Whales Retail Pro | $75/mo, $63/mo annual | | same |
| Cheddar Flow | $85-99/mo | | secondary |
| FlowAlgo | $149/mo, $99/mo annual | | secondary |
| Polytraders founder tier | **$24.50/mo locked for life** | pre-launch, waitlist, Q1 2027 launch; claims "588k Medium-to-Heavy Polymarket traders" | [polytraders.com](https://polytraders.com/) |
| Polytraders Pro | 0.25% of trades, capped $99/mo | vendor claim of "$30B monthly volume across Polymarket + Kalshi (Jun 2026)" **contradicts** the brief's $13.7B June peak and DefiRate's tracker: **unverified** | same |
| Kalshi bot service | $99/mo | | secondary |
| Turbine Pro | $199/mo | | secondary |
| Kalshi API | free | | secondary |
| Polymarket SDK + VPS | free SDK, $5-20/mo VPS | | secondary |
| Kalshi institutional L1/L2 multicast feed | price on request; Kalshi waives its revenue share year 1 | targets market makers and systematic firms | [Finance Magnates](https://www.financemagnates.com/fintech/kalshi-launches-real-time-level-2-data-feed-for-institutional-traders/), 2026-08-12 |
| Hyperliquid builder codes | **up to 0.1% per perp trade, 1% spot**; 100 USDC minimum | $40M paid to builders; 40% of DAU on third-party frontends; Phantom ~$100k/day; PVP.trade $7.2M lifetime | [Dwellir](https://www.dwellir.com/blog/hyperliquid-builder-codes), **date and per-app figures unverified** |
| Prop-firm back-office SaaS (for sale) | **$180,000 average annual order value per client** | $6,172,735 gross income, $3,351,572 EBITDA, 50+ enterprise clients, 1M+ active traders, founded 2023, asking $28,500,000 | [Website Closers listing 118819](https://www.websiteclosers.com/businesses/software-platform-for-saas-infrastructure-tech-stack-for-saas-prop-trading-firms-50-enterprise-clients-180k-aov-1-million-active-traders/118819/), **listing date not stated** |
| Prop firm evaluation spend | $4,270 average per trader before profitability | 2.1M funded traders, 12M annual challenge signups | brief; [track360](https://track360.io/blog/prop-trading-industry-report-2026-market-analysis) |
| Bespoke TPO merge/split indicator | **$30-100 one time** | | [MQL5 freelance job 113445](https://www.mql5.com/en/job/113445) |
| chart-img.com | **unverified**, still client-rendered; rate caps are BASIC 50/day, PRO 500/day, MEGA 1,000/day, ULTRA 3,000/day, ENTERPRISE 5,000+/day | | [doc.chart-img.com](https://doc.chart-img.com/) |
| vectorbt PRO | $25/mo, $500 lifetime | | prior report |
| Databento | $199 / $1,750 / $4,500 per month | | prior report |
| Norgate US Stocks Platinum | $630/yr | | prior report |

**What this table says, in three lines.**

The retail chart band is $12.95 to $99 a month and it is fully occupied by products that already ship your feature.
The one visualization business here with a disclosed number, Bookmap, does $10.3M with 55 people, bootstrapped, after more than a decade, and that is the ceiling of the category you were about to enter.
The two business models in this table that are structurally better than a chart subscription are the **$180,000-per-client B2B infrastructure sale to prop firms** and the **per-trade builder fee on Hyperliquid**, and neither has anything to do with rendering.

One more calibration point, and it is the cruellest one.
The observed market price for a human to hand-build TPO merge and split from scratch is **$30 to $100, once**, on MQL5.
That is your product's replacement cost.

---

## 6. The case against, stated as strongly as I can make it

I already put the verdict first, as instructed. Here is the argument at full strength, in the order that a sceptic should read it.

**The feature exists, is six years old, and is free.**
Sierra Chart shipped split columns before November 2020. TradingView shipped split and merge in November 2025. Quantower advertises "Separate the profile into each column" on a marketing page. MotiveWave, ATAS and Bookmap all have their own versions. Not one of them sells it separately. You would be launching a paid product against five free implementations, the oldest of which predates it by six years.

**The demand you would be serving does not appear anywhere it would appear if it existed.**
447 votes for a risk module, 24 for profile split-and-merge. 4,549 boosts for a footprint script, 293 for a TPO script. 405 stars for the biggest market profile library, unmaintained since 2023, against 102,574 for a single LLM trading framework. Not one visualization tool in a 444-star curated list of 30+ MCP servers and 40+ agent frameworks. Six independent channels, all pointing the same direction.

**The agent buyer, the whole reason "for agents" is in the title, is structurally incapable of wanting this.**
2026's shipped agent infrastructure is MCP servers that return JSON. An agent asks a broker MCP for a price and gets a number. The image only exists if you insert a rendering step that throws away precision the agent already had. The two largest agent frameworks in the world, at 102.6k and 22.2k stars, consume no images at all. This is not a marketing gap. It is an architecture that has no slot for your product.

**A better rendering probably cannot fix what is broken.**
The best mechanistic study of VLM chart failure found the vision encoder already extracts correct coordinates and the failure is in the vision-language handoff. Your product changes the input to the half that works.

**The price ceiling is below the cost of building it.**
$26 a month for Sierra Chart. $49 a month for all of Quantower. $30 to $100 to have the feature built bespoke. Bookmap, the best-executed pure visualization business in this space, is at $10.3M after a decade with 55 staff.

**And the strongest form of the argument is this.**
Even if you win question 3 outright, even if a profile rendering measurably beats a candlestick for VLM reading, you have proven a fact about a representation, not built a business. The party who would monetise that fact is whoever is already selling agents their market data as JSON, and they would implement it in an afternoon by changing a renderer they already own. There is no defensible position between "the finding" and "the platform." You would be doing free research for Sierra Chart.

The only counter I can construct that survives is narrow: the machine-legibility question is genuinely unanswered, testing it is cheap, and being the person who answered it first has option value. That is an argument for a two-week experiment, not for a product.

---

## What remains unknown, and what it would cost to resolve

1. **Whether a profile, footprint or heatmap rendering actually beats a candlestick for a VLM.** Nobody has tested it; I verified the one paper that would have used candlesticks only. **To resolve:** render the same N windows five ways (candlestick, TPO profile, split-column TPO profile, footprint, numeric table), ask one frontier VLM the same direction question over each, and compare. Roughly 5,000 images, a few hundred dollars of API spend, one to two weeks of engineering. This is the single highest-value open item in the brief and it is nearly free.
2. **Whether the EncQA encoding hierarchy transfers to financial renderings.** My prediction that a length-encoded profile reads better than a position-non-aligned candlestick, and that a colour-encoded heatmap reads worst, is inference from a synthetic benchmark. **To resolve:** add a market-profile condition to [apple/ml-encqa](https://github.com/apple/ml-encqa), which is open. Days, not weeks.
3. **Quantower's standalone TPO add-on price.** Client-rendered. **To resolve:** install the free Quantower client and read the in-app store. Thirty minutes, and it is the only direct price a customer has ever paid specifically for split-profile functionality.
4. **The real size of the prop-firm industry.** The brief says $20B, the best 2026 estimate I found says $850M revenue, and the methodology behind the $850M is ad-scraping and LinkedIn headcount. A 23x disagreement changes every downstream conclusion. **To resolve:** reconcile the definitions. FTMO and FundedNext file accounts in some jurisdictions; pull them.
5. **Topstep's actual funded-account automation rules, verbatim.** `topstep.com/rules/` returned 404 on 2026-09-05 and every source I have is secondary. This determines whether an agent can legally run on a funded prop account, which is the load-bearing fact under the entire prop-firm direction. **To resolve:** open a Topstep account and read the rules page from inside. Under an hour.
6. **Kalshi's institutional feed price.** "Available upon request." **To resolve:** request it. It tells you what a systematic firm actually pays for prediction-market microstructure, which is the only hard willingness-to-pay number in the fastest-growing agent venue.
7. **Whether the Hyperliquid builder-code figures are real.** $40M cumulative, $100k/day for Phantom and $7.2M for PVP.trade all come from one vendor blog with no publication date. **To resolve:** the [Blockworks builder-fee dashboard](https://blockworks.com/analytics/hyperliquid/hyperliquid-builder-codes/hyperliquid-builder-codes-trading-builder-fees) is on-chain and free. An afternoon.
8. **Whether SEBI-style algo rules are landing outside India.** The audit-trail lane is only large if the regulation generalises. **To resolve:** check ESMA, the FCA and CFTC rulemaking calendars for algo-identifier and agent-accountability proposals.
9. **The full ATAS board below rank 20, and equivalent boards for Bookmap, Sierra Chart and NinjaTrader.** My ranking rests on one vendor's top 20. Bookmap's forum returned 403 and Sierra Chart has no vote mechanism. **To resolve:** browser-driven scraping of the paginated boards. A day, and it would either confirm or break section 2.
10. **Reddit, still unreached.** Four routes failed on 2026-09-05: direct fetch, the search domain filter, PullPush (now paywalled: "This website does not provide free scraping resources for agents"), and two mirrors. **To resolve:** drive a real browser session, or solve the Anubis proof-of-work on `safereddit.com`, which is set to difficulty 2 and is computationally trivial. An hour of engineering, and I judged it not worth the detour given that vote-counted boards answer the same question better.

---

## What I would build, ranked

**0. Nothing in the direction of the brief.** Do not build a split x-axis market profile, for agents or for humans. It exists in five products, the oldest since 2020, none of them charges for it, the community ranks it 20th, and the agent ecosystem has no slot for an image. Confidence: high.

**1. A two-week falsification experiment, not a product.** Render identical windows as candlestick, TPO, split-column TPO, footprint and numeric table; run one frontier VLM over all five; publish the result either way. Cost: a few hundred dollars of API spend and one to two weeks. If profile beats candlestick by a wide margin you have a paper, a benchmark contribution to EncQA or Agent Trading Arena, and a genuine reason to revisit. If it does not, you have closed the most decision-relevant question in this brief permanently, for the price of a dinner. This is the only item on this list I would start on Monday. Confidence that it is worth doing: high. Confidence that it produces a product: low.

**2. If you want a business rather than an answer: a Hyperliquid builder-code surface.** It is the only venue in this report where a third party is paid per trade rather than per seat, the rail already paid out $40M, 40% of daily active users already prefer third-party frontends, and the entry requirement is 100 USDC. You would be monetising order flow, not pixels. Verify the volume trend first: perp DEX volumes are down over 50% since October. Confidence: medium, contingent on item 7 above.

**3. Execution guardrails and audit trails for trading agents.** The largest cluster on the only vote-counted board I could reach is risk management (588 votes across three items), the largest curated agent list names execution guardrails and audit trails as gaps, and SEBI made Algo-IDs, kill switches and periodic performance disclosure legally mandatory on 2026-04-01. This is the only demand in the report with a regulator behind it rather than a forum. Confidence: medium, contingent on item 8.

**4. Nothing else.** Do not build a rendering API (chart-img caps its own enterprise tier at 5,000 images a day, which tells you the size of that demand). Do not build a prediction-market terminal (Kalshi is building the missing layer itself and waiving the fee). Do not build another order-flow chart (Bookmap is the ceiling and it is $10.3M).

The prior scout's report ended by keeping "the visual in the pitch" on the strength of the multimodal-agent market.
That reason is gone. The multimodal agent market, as it actually shipped in 2026, is a JSON API.
What replaced it is a narrower and more interesting question about whether human chart conventions are machine-hostile, and the right response to that question is an experiment that costs a few hundred dollars, not a product that costs a year.
