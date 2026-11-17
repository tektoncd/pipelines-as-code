---
title: Best Practices and Troubleshooting
weight: 3
---

This page provides recommendations for writing effective prompts, securing API keys, managing costs, and troubleshooting LLM-powered analysis. Follow these practices to get reliable, cost-efficient results from your LLM provider.

## Prompt Engineering

The prompt you write in each analysis role determines the quality and usefulness of the LLM response. Follow these guidelines:

1. **Be specific**: Tell the LLM provider exactly what you want it to produce.
2. **Structure your prompts**: Use numbered lists for clarity.
3. **Set expectations**: Define the output format you expect.
4. **Provide context**: Explain what information Pipelines-as-Code sends along with the prompt.

Example prompt:

```yaml
prompt: |
  You are a DevOps expert analyzing a CI/CD pipeline failure.

  Based on the error logs and context provided:
  1. Identify the root cause of the failure
  2. Suggest 2-3 specific steps to fix the issue
  3. Recommend one preventive measure for the future

  Keep your response concise and actionable.
```

## Security Considerations

Because LLM-powered analysis sends pipeline data to an external service, take these precautions:

1. **Protect API keys**: Always store them in Kubernetes secrets, never in plain text.
2. **Review log content**: Understand what log data Pipelines-as-Code sends to the LLM provider, and avoid including secrets or credentials in pipeline output.
3. **Set up billing alerts**: Configure alerts with your LLM provider to detect unexpected usage.
4. **Configure timeouts**: Use the `timeout_seconds` setting to prevent runaway requests.

## Cost Management

Every LLM request consumes tokens, and your LLM provider charges based on token usage. The more context you send and the longer the response, the higher the cost. Keep these strategies in mind:

1. **Select appropriate models**: Use more economical models for simple tasks and reserve expensive models for complex analysis. Consult your LLM provider's pricing documentation.
2. **Limit `max_tokens`**: Set a lower value to cap response length and reduce per-request cost.
3. **Use selective triggers**: Configure `on_cel` expressions so Pipelines-as-Code only triggers analysis on failures, not on every run.
4. **Control log lines**: Keep `max_lines` in `container_logs` low to reduce the amount of context sent to the LLM provider.

## Performance Tips

LLM analysis runs in the background and does not block your pipeline. However, large context payloads can slow down the request or cause timeouts.

1. **Set reasonable timeouts**: The default of 30 seconds is usually sufficient for most analyses.
2. **Include only relevant context**: Enable only the context items you actually need for your prompt.
3. **Limit log fetching**: Setting `container_logs.max_lines` above 500 can degrade performance when Pipelines-as-Code fetches logs from many containers. Start with 50-100 and increase only if needed.
4. **Monitor failures**: If analysis consistently fails, check the Pipelines-as-Code watcher logs for errors.

## Troubleshooting

If LLM-powered analysis does not produce the results you expect, start with the checks below.

### Analysis Not Running

Verify the following:

- You set `enabled: true` in the `spec.settings.ai` configuration.
- The CEL expression in `on_cel` matches your event (test it with a known-failing PipelineRun).
- The API key secret exists and is accessible in the same namespace.
- The namespace of the secret matches the namespace of the Repository CR.

### API Errors

If Pipelines-as-Code receives an error from the LLM provider, check these common causes:

- **401 Unauthorized**: Your API key is invalid or expired. Regenerate it and update the Kubernetes secret.
- **429 Rate Limited**: You are exceeding the LLM provider's rate limits. Reduce analysis frequency or upgrade your plan.
- **Timeout**: The request took too long. Increase `timeout_seconds` or reduce context size.

To inspect detailed error messages, check the watcher logs:

```bash
kubectl logs -n pipelines-as-code deployment/pipelines-as-code-watcher | grep "LLM"
```

### High Costs

If your LLM provider bills are higher than expected, take these steps to reduce token consumption:

1. Use more restrictive `on_cel` expressions so analysis runs less frequently.
2. Lower the `max_tokens` value to shorten responses.
3. Reduce `container_logs.max_lines` to send less context per request.
4. Switch to a more economical model for roles that do not require deep analysis.

## Limitations

- LLM-powered analysis is best-effort and non-blocking; it does not affect pipeline success or failure status.
- You are responsible for all API key costs charged by your LLM provider.
- Analysis is subject to the rate limits of your LLM provider.
- Context size is limited by the provider's token constraints.
- Do not send sensitive or confidential log data to external LLM providers.

## Further Reading

- [Repository CR Reference]({{< relref "/docs/guides/repository-crd" >}})
- [CEL Expressions Guide]({{< relref "/docs/guides/event-matching/cel-expressions" >}})
- [Sample Configurations](https://github.com/tektoncd/pipelines-as-code/tree/main/samples)
