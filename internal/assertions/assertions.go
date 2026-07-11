package assertions

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/jagadishg/arbor/internal/model"
	"github.com/jagadishg/arbor/internal/responsevalue"
	"github.com/jagadishg/arbor/internal/variables"
)

var expressionPattern = regexp.MustCompile(`^\s*(.+?)\s+(==|!=|>=|<=|>|<|contains)\s+(.+?)\s*$`)

func EvaluateAll(expressions []string, response *model.Response, vars *variables.Set) []model.AssertionResult {
	results := make([]model.AssertionResult, 0, len(expressions))
	for _, expression := range expressions {
		results = append(results, Evaluate(expression, response, vars))
	}
	return results
}

func Evaluate(expression string, response *model.Response, vars *variables.Set) model.AssertionResult {
	result := model.AssertionResult{Expression: expression}
	match := expressionPattern.FindStringSubmatch(expression)
	if match == nil {
		result.Message = "expected: <selector> <operator> <value>"
		return result
	}
	left, err := responsevalue.Select(response, match[1])
	if err != nil {
		result.Message = err.Error()
		return result
	}
	rawRight, err := vars.Resolve(match[3])
	if err != nil {
		result.Message = err.Error()
		return result
	}
	right := parseLiteral(rawRight)
	passed, err := compare(left, match[2], right)
	if err != nil {
		result.Message = err.Error()
		return result
	}
	result.Passed = passed
	if !passed {
		result.Message = fmt.Sprintf("expected %s %s %s; got %s", match[1], match[2], rawRight, responsevalue.String(left))
	}
	return result
}

func parseLiteral(raw string) any {
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		return value
	}
	if strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") && len(raw) >= 2 {
		return strings.Trim(raw, "'")
	}
	return raw
}

func compare(left any, operator string, right any) (bool, error) {
	if operator == "contains" {
		return strings.Contains(responsevalue.String(left), responsevalue.String(right)), nil
	}
	if leftNumber, ok := number(left); ok {
		if rightNumber, ok := number(right); ok {
			switch operator {
			case "==":
				return leftNumber == rightNumber, nil
			case "!=":
				return leftNumber != rightNumber, nil
			case ">":
				return leftNumber > rightNumber, nil
			case "<":
				return leftNumber < rightNumber, nil
			case ">=":
				return leftNumber >= rightNumber, nil
			case "<=":
				return leftNumber <= rightNumber, nil
			}
		}
	}
	switch operator {
	case "==":
		return reflect.DeepEqual(left, right) || responsevalue.String(left) == responsevalue.String(right), nil
	case "!=":
		return !(reflect.DeepEqual(left, right) || responsevalue.String(left) == responsevalue.String(right)), nil
	default:
		return false, fmt.Errorf("operator %q requires numeric operands", operator)
	}
}

func number(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}
