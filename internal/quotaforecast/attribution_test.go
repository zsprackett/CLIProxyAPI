package quotaforecast

import (
	"math"
	"testing"
)

// syntheticPool builds one account whose hourly burn is exactly
// 2pp per 1M "big" tokens plus 0.5pp per 1M "small" tokens.
func syntheticPool(hours int) ([]member, []UsageBucket) {
	var points []Point
	var usage []UsageBucket
	used := 0.0
	points = append(points, Point{T: 0, Used: 0, Reset: 1_000 * hour})
	for h := 0; h < hours; h++ {
		big := int64((h%4 + 1) * 1_000_000)
		small := int64((h%3 + 1) * 2_000_000)
		usage = append(usage,
			UsageBucket{Hour: int64(h) * hour, AuthID: "a", Model: "big", KeyHash: "k1", Agent: "cli", Requests: 1, Tokens: big},
			UsageBucket{Hour: int64(h) * hour, AuthID: "a", Model: "small", KeyHash: "k2", Agent: "ide", Requests: 1, Tokens: small},
		)
		used += 2*float64(big)/1e6 + 0.5*float64(small)/1e6
		points = append(points, Point{T: int64(h+1) * hour, Used: used, Reset: 1_000 * hour})
	}
	return []member{{AuthID: "a", Points: points}}, usage
}

func TestAttributeRecoversPerModelCost(t *testing.T) {
	members, usage := syntheticPool(24)
	attr := attribute(members, usage, map[string]string{"k1": "sk-1...abcd"}, 24*hour)
	if attr.Method != "nnls" || attr.Rows != 24 || attr.R2 == nil || *attr.R2 < 0.99 {
		t.Fatalf("attribution = %+v", attr)
	}
	rates := map[string]float64{}
	var share float64
	for _, row := range attr.Models {
		rates[row.Model] = row.PercentPerMTok
		share += row.Share
	}
	if math.Abs(rates["big"]-2) > 0.01 || math.Abs(rates["small"]-0.5) > 0.01 {
		t.Fatalf("per-model rates = %v, want big=2 small=0.5", rates)
	}
	if math.Abs(share-1) > 0.001 {
		t.Fatalf("model shares sum to %v", share)
	}
	if attr.Clients[0].KeyHash != "k1" || attr.Clients[0].KeyMasked != "sk-1...abcd" {
		t.Fatalf("clients = %+v", attr.Clients)
	}
	if attr.Agents[0].Agent != "cli" {
		t.Fatalf("agents = %+v", attr.Agents)
	}
}

func TestAttributeFallsBackToProportional(t *testing.T) {
	members, usage := syntheticPool(4)
	attr := attribute(members, usage, nil, 4*hour)
	if attr.Method != "proportional" {
		t.Fatalf("method = %s, want proportional with only 4 hours", attr.Method)
	}
	// Each hour's burn is split by token share, so the totals still add up.
	if burn := attr.Models[0].BurnAccounts + attr.Models[1].BurnAccounts; math.Abs(burn-0.27) > 1e-3 {
		t.Fatalf("allocated burn = %v, want all 27pp: %+v", burn, attr.Models)
	}
	if got := attribute(members, nil, nil, 4*hour); got.Method != "none" {
		t.Fatalf("no usage → method %s", got.Method)
	}
}
