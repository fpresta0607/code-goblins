# Brief: is there a market for a trading-visualization product for agents?

You are a market scout. Your job is evidence, not encouragement.
The Overlord is deciding whether to build a **split x-axis market profile / window-trader visualization** aimed at trading agents, and wants to know whether the market needs it at all, and if not, where the real demand is.

Write for a reader who will spend money based on this. Every claim gets a source and a date. Anything you cannot verify at the source, mark **unverified**. Do not use em dashes.

## Read this first

`C:\dev\code-goblins\data\trading-visual-training-scout\report.md` is a prior scout report on visual-first training data for trading agents. Read it in full. Its conclusion was: do not sell a dataset, sell a reproducible corpus generator at vectorbt pricing ($25/mo, $500 lifetime), and the multimodal-agent market is "the only reason to keep the visual in the pitch at all."

**That last claim is now falsified.** Start from that.

## What I already established (do not re-search this, build on it)

**The visual thesis is empirically dead as of 2026.**
- arXiv 2604.12659, "Do VLMs Truly 'Read' Candlesticks?" 193,524 charts (96,770 daily / 96,754 weekly), 300 HS300 + 500 S&P500 stocks, 2015-2025. Best model Claude-Sonnet-4-5 thinking 53.48%; Gemini-2.5-Pro 52.44%; XGBoost numeric baseline 50.87%; GPT-4o 49.40%. Paper: "most VLMs perform well only under persistent uptrend or downtrend conditions." No clean image-vs-numeric ablation, which is a gap you may be able to close.
- Independent audit (gist roman-rr/c1cd675f7c35b68ae5ac281c30080166), April 2026: 4 models, 2 vendors, 40 verified crypto signals, 215 calls. Direction 51.4-57.1%, every CI contains 50%. Pattern-name accuracy **1 correct out of 215**. Gemini 3 Flash long-bias gap **90 percentage points** (17/17 long, 2/20 short). Conclusion: "no frontier vision LLM we tested is capable of reliable directional chart-pattern analysis."
- nof1.ai Alpha Arena, real money, $10k per model on Hyperliquid perps. Season 1: ChatGPT -$6,267, Gemini -$5,671, Grok -$4,531, Claude Sonnet -$3,081. Founder Jay Azhang: "Handing money directly to an LLM and letting it trade on its own, that path doesn't work yet."

**Agent-to-broker infrastructure is the thing that actually shipped in 2026.**
Five first-party broker MCP servers launched in 2026: TradeStation (January, first, requires paid Claude Pro or ChatGPT Plus and $10,000 balance), Interactive Brokers (July, free, drafts orders but user submits), Webull (free, places orders under user caps), Public.com (free, $20 minimum), Robinhood (free, separate agent account). Schwab, E*TRADE, tastytrade are community-built only. Source: stockbrokers.com/guides/ai-agent-brokers.

**Prediction markets are the fastest-growing agent venue.**
Kalshi + Polymarket combined monthly volume went from under $5B (Sept 2025) to about $24B (April 2026); peaked $13.7B in June; Kalshi annualized $178B. On-chain prediction volume $36B in Q1 2026. **14 of the 20 most profitable Polymarket wallets are bots.** But Prediction Arena benchmark (Jan 12 - Mar 9) had six AI models lose 16% to 30.8% of capital on Kalshi and average -1.1% on Polymarket.

**The futures prop-firm ecosystem is where the visualization buyer actually lives, and it is enormous.**
Industry valued at $20B across 2,000+ firms. Average evaluation spend **$4,270 per trader**. Evaluation fees are **80-95% of firm revenue**. Pass rates 5-10% (Apex 15-20%, 40% with resets). Only 7% of funded accounts ever receive a payout; 60% lose their capital. Apex has paid $598M since 2022, averaging $15.4M/month late 2025. Search interest up 5,556% since 2020. Over 80 firms closed in 2024.
**Critical constraint:** Apex permits bots during evaluation but prohibits fully automated "set and forget" on funded accounts, requiring active management. Reporting suggests "we allow automation" is widely hedged by carve-outs that exclude most real algorithms.

**Order-flow tooling is a real market with real prices**, notably above the $25-60 band the prior report assumed: Sierra Chart $26-56/mo, Bookmap $16-79/mo plus $990/$1,990 lifetime, ATAS ~$85/mo, NinjaTrader $99/mo, data feeds from ~$12/mo per exchange.

**Definition, confirmed:** a market profile / TPO chart's x-axis is visited time per price, not time. A "split profile" divides the profile into separate columns per period so each letter column shows its own high, low, open and close.

**Ecosystem gaps flagged in LLMQuant/awesome-trading-agents:** execution guardrails, regulatory reporting and audit trails, cost/slippage modeling, and live performance transparency.

## What I need you to find out

1. **Does anyone sell a split x-axis / windowed market profile today, and does anyone want one?** Search the incumbents by feature, not by name: Sierra Chart, Bookmap, ATAS, Quantower, Exocharts, MotiveWave, Jigsaw, GoCharting, Tradovate, TradingView. Is split-profile a checkbox everyone already has, or a genuine gap? Find feature-request threads, forum posts, changelogs.

2. **The community demand signal.** I could not reach Reddit directly (blocked to the crawler). Get at it another way: Reddit mirrors and archives, Discord and forum summaries, Elite Trader, futures.io, QuantConnect and NinjaTrader forums, TradingView ideas and script popularity, GitHub stars on market-profile repos, Hugging Face and Kaggle. What do futures day traders and algo traders actually ask for repeatedly? Rank the top recurring asks by evidence of frequency.

3. **Is there a machine-legibility angle?** The VLM results say models cannot read candlesticks. Two readings: (a) vision is a dead end for market data, or (b) candlesticks are a human-legible format that happens to be machine-hostile, and a representation designed for machine reading could do better. Find any evidence either way. Has anyone tested VLMs on profile/heatmap/footprint renderings rather than candlesticks? Is there a benchmark to enter? This is the single most decision-relevant question in the brief.

4. **What is the most demanding market in the trading space right now for agents?** Rank candidates by money actually moving and by unmet need, not by hype: prediction markets, crypto perps, futures prop evaluation, options flow, equities research, execution/TCA, compliance and audit. For the top two, say concretely who pays, how much, and what they are buying today.

5. **Willingness to pay, evidenced.** For whichever direction survives, find comparable products and their real prices and, if discoverable, their scale (subscriber counts, revenue, app-store or Gumroad numbers, GitHub sponsors). The prior report's weakest section was "no comparable found."

6. **The case against, stated as strongly as you can make it.** If the answer is "do not build this," say so in the first paragraph and prove it.

## Deliverable

Write your report to `C:\dev\code-goblins\data\trading-market-demand-scout\report.md` in the **primary repo**, not inside your worktree, and also commit and push it on your branch. A prior scout wrote its report inside its worktree and `cfo cleanup` destroyed it.

Structure: verdict first with the decision and the confidence you have in it, then the evidence, then a section of what remains unknown and what it would cost to resolve. Include a table of every price you find. End with a ranked list of what you would build, or an explicit recommendation to build nothing.

Report done with `cfo notify trading-market-demand-scout --done --pr <url-or-path>`.
