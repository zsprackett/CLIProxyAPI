package quotaforecast

import (
	"sort"
)

const (
	// nnlsMinRows is the number of hours with quota burn needed before per-model
	// costs are fitted; with less data every token is assumed to cost the same.
	nnlsMinRows = 12
	// nnlsMinRowsPerModel is how many hours each model must appear in to be fitted.
	nnlsMinRowsPerModel = 3
	nnlsIterations      = 500
)

type attrCell struct {
	model    string
	keyHash  string
	agent    string
	tokens   float64
	requests int64
}

type attrRow struct {
	burn  float64
	cells []attrCell
}

// attribute estimates how much of the pool's burn each model, client key and client
// agent caused. Each (credential, hour) with token usage is one observation: its
// quota burn is regressed on the uncached tokens per model with non-negative least
// squares, and the burn is then split across the hour's requests by modelled cost.
func attribute(members []member, usage []UsageBucket, maskedKeys map[string]string, now int64) *Attribution {
	byAuth := make(map[string]member, len(members))
	for _, m := range members {
		byAuth[m.AuthID] = m
	}
	type rowKey struct {
		auth string
		hour int64
	}
	grouped := make(map[rowKey][]attrCell)
	for _, b := range usage {
		m, ok := byAuth[b.AuthID]
		if !ok || b.Tokens <= 0 || b.Hour < m.Points[0].T-3600 || b.Hour >= now {
			continue
		}
		key := rowKey{b.AuthID, b.Hour}
		grouped[key] = append(grouped[key], attrCell{model: b.Model, keyHash: b.KeyHash, agent: b.Agent, tokens: float64(b.Tokens), requests: b.Requests})
	}
	keys := make([]rowKey, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].auth != keys[j].auth {
			return keys[i].auth < keys[j].auth
		}
		return keys[i].hour < keys[j].hour
	})
	rows := make([]attrRow, 0, len(keys))
	for _, key := range keys {
		rows = append(rows, attrRow{burn: accountBurn(byAuth[key.auth].Points, key.hour, key.hour+3600), cells: grouped[key]})
	}
	if len(rows) == 0 {
		return &Attribution{Method: "none"}
	}

	models := modelIndex(rows)
	x := make([][]float64, len(rows))
	y := make([]float64, len(rows))
	burnRows := 0
	modelRows := make([]int, len(models))
	for i, row := range rows {
		x[i] = make([]float64, len(models))
		for _, c := range row.cells {
			x[i][models[c.model]] += c.tokens
		}
		y[i] = row.burn
		if row.burn > 0 {
			burnRows++
		}
		for m, v := range x[i] {
			if v > 0 {
				modelRows[m]++
			}
		}
	}
	method := "nnls"
	if burnRows < nnlsMinRows {
		method = "proportional"
	}
	for _, count := range modelRows {
		if count < nnlsMinRowsPerModel {
			method = "proportional"
		}
	}
	var beta []float64
	if method == "nnls" {
		beta = fitNNLS(x, y)
	} else {
		beta = fitProportional(x, y)
	}

	out := &Attribution{Method: method, Rows: len(rows), R2: rSquared(x, y, beta)}
	modelAgg := make(map[string]*AttrRow)
	clientAgg := make(map[string]*AttrRow)
	agentAgg := make(map[string]*AttrRow)
	var totalBurn float64
	for _, row := range rows {
		costs := make([]float64, len(row.cells))
		var costSum, tokenSum float64
		for i, c := range row.cells {
			costs[i] = beta[models[c.model]] * c.tokens
			costSum += costs[i]
			tokenSum += c.tokens
		}
		for i, c := range row.cells {
			share := c.tokens / tokenSum
			if costSum > 0 {
				share = costs[i] / costSum
			}
			burn := row.burn * share
			totalBurn += burn
			accumulate(modelAgg, c.model, AttrRow{Model: c.model}, c, burn)
			accumulate(clientAgg, c.keyHash, AttrRow{KeyHash: c.keyHash, KeyMasked: maskedKeys[c.keyHash]}, c, burn)
			accumulate(agentAgg, c.agent, AttrRow{Agent: c.agent}, c, burn)
		}
	}
	out.Models = finishAttrRows(modelAgg, totalBurn)
	out.Clients = finishAttrRows(clientAgg, totalBurn)
	out.Agents = finishAttrRows(agentAgg, totalBurn)
	return out
}

func modelIndex(rows []attrRow) map[string]int {
	var names []string
	seen := make(map[string]bool)
	for _, row := range rows {
		for _, c := range row.cells {
			if !seen[c.model] {
				seen[c.model] = true
				names = append(names, c.model)
			}
		}
	}
	sort.Strings(names)
	index := make(map[string]int, len(names))
	for i, name := range names {
		index[name] = i
	}
	return index
}

// fitNNLS solves min ||y - X·beta||² subject to beta >= 0 by cyclic coordinate descent.
func fitNNLS(x [][]float64, y []float64) []float64 {
	cols := len(x[0])
	beta := fitProportional(x, y)
	norms := make([]float64, cols)
	for _, row := range x {
		for j, v := range row {
			norms[j] += v * v
		}
	}
	residual := make([]float64, len(y))
	for i, row := range x {
		residual[i] = y[i]
		for j, v := range row {
			residual[i] -= beta[j] * v
		}
	}
	for iter := 0; iter < nnlsIterations; iter++ {
		for j := 0; j < cols; j++ {
			if norms[j] == 0 {
				continue
			}
			var dot float64
			for i, row := range x {
				dot += row[j] * (residual[i] + beta[j]*row[j])
			}
			next := dot / norms[j]
			if next < 0 {
				next = 0
			}
			if delta := next - beta[j]; delta != 0 {
				for i, row := range x {
					residual[i] -= delta * row[j]
				}
				beta[j] = next
			}
		}
	}
	return beta
}

// fitProportional assumes every token costs the same: total burn over total tokens.
func fitProportional(x [][]float64, y []float64) []float64 {
	var tokens, burn float64
	for i, row := range x {
		for _, v := range row {
			tokens += v
		}
		burn += y[i]
	}
	beta := make([]float64, len(x[0]))
	if tokens > 0 {
		for j := range beta {
			beta[j] = burn / tokens
		}
	}
	return beta
}

func rSquared(x [][]float64, y []float64, beta []float64) *float64 {
	if len(y) < 2 {
		return nil
	}
	var mean float64
	for _, v := range y {
		mean += v
	}
	mean /= float64(len(y))
	var ssRes, ssTot float64
	for i, row := range x {
		var pred float64
		for j, v := range row {
			pred += beta[j] * v
		}
		ssRes += (y[i] - pred) * (y[i] - pred)
		ssTot += (y[i] - mean) * (y[i] - mean)
	}
	if ssTot == 0 {
		return nil
	}
	return floatPtr(round4(1 - ssRes/ssTot))
}

func accumulate(agg map[string]*AttrRow, key string, base AttrRow, c attrCell, burn float64) {
	row := agg[key]
	if row == nil {
		row = &base
		agg[key] = row
	}
	row.Tokens += int64(c.tokens)
	row.Requests += c.requests
	row.BurnAccounts += burn / 100
}

func finishAttrRows(agg map[string]*AttrRow, totalBurn float64) []AttrRow {
	out := make([]AttrRow, 0, len(agg))
	for _, row := range agg {
		if row.Tokens > 0 {
			row.PercentPerMTok = round4(row.BurnAccounts * 100 / float64(row.Tokens) * 1e6)
		}
		if totalBurn > 0 {
			row.Share = round4(row.BurnAccounts * 100 / totalBurn)
		}
		row.BurnAccounts = round4(row.BurnAccounts)
		out = append(out, *row)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BurnAccounts != out[j].BurnAccounts {
			return out[i].BurnAccounts > out[j].BurnAccounts
		}
		return out[i].Tokens > out[j].Tokens
	})
	return out
}
