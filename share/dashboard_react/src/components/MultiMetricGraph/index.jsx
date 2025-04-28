import React, { useEffect, useRef } from 'react';
import * as d3 from 'd3';

/**
 * MultiMetricGraph component for displaying multiple metrics in a single graph
 * Compatible with existing Graphite/Cubism setup
 */
function MultiMetricGraph({
  chartRef,
  size,
  step,
  context,
  title,
  metrics,
  maxExtent,
  className,
  showPercentage = false
}) {
  const graphRef = useRef(null);

  // Link the ref if provided
  useEffect(() => {
    if (chartRef) {
      chartRef.current = graphRef.current;
    }
  }, [chartRef]);

  useEffect(() => {
    if (!context || !graphRef.current || !metrics || metrics.length === 0) return;

    // Clear any existing content
    d3.select(graphRef.current).selectAll("*").remove();

    const width = size;
    const height = 120;
    const margin = { top: 20, right: 80, bottom: 30, left: 50 };

    const svg = d3.select(graphRef.current)
      .append("svg")
      .attr("width", width + margin.left + margin.right)
      .attr("height", height + margin.top + margin.bottom)
      .attr("class", "multi-metric-graph-svg")
      .append("g")
      .attr("transform", `translate(${margin.left},${margin.top})`);

    // Create title
    svg.append("text")
      .attr("x", 0)
      .attr("y", -5)
      .attr("class", "title")
      .text(title);

    // Create a simple rule for the current time
    const rule = svg.append("g")
      .attr("class", "rule")
      .style("display", "none");

    rule.append("line")
      .attr("y1", 0)
      .attr("y2", height);

    rule.append("text")
      .attr("y", -10)
      .attr("text-anchor", "middle");

    // Define timescale using Cubism context
    const start = new Date(context.start());
    const stop = new Date(context.stop());

    const xScale = d3.scaleTime()
      .domain([start, stop])
      .range([0, width]);

    // Setup the metrics
    const metricDatasets = metrics.map(metric => ({
      ...metric,
      graphiteMetric: context.graphite(metric.target)
    }));

    // Setup scales
    let yDomain = [0, 0];

    // If we have a maxExtent metric, set it as our max y value
    const maxExtentMetric = metricDatasets.find(d => d.isMaxExtent);

    if (maxExtentMetric) {
      const maxExtentValues = [];

      // We need to collect values across the time range
      for (let i = 0; i < size; i++) {
        const value = maxExtentMetric.graphiteMetric.valueAt(i);
        if (value !== null && value !== undefined) {
          maxExtentValues.push(value);
        }
      }

      if (maxExtentValues.length > 0) {
        yDomain[1] = d3.max(maxExtentValues) * 1.1; // Add 10% padding
      }
    }

    // If no max extent is defined, find the max value across all metrics
    if (yDomain[1] === 0) {
      const allValues = [];

      metricDatasets.forEach(dataset => {
        for (let i = 0; i < size; i++) {
          const value = dataset.graphiteMetric.valueAt(i);
          if (value !== null && value !== undefined) {
            allValues.push(value);
          }
        }
      });

      if (allValues.length > 0) {
        yDomain[1] = d3.max(allValues) * 1.1; // Add 10% padding
      } else {
        yDomain[1] = 100; // Default if no data
      }
    }

    const yScale = d3.scaleLinear()
      .domain(yDomain)
      .range([height, 0]);

    // Create axes
    const xAxis = d3.axisBottom(xScale)
      .ticks(5)
      .tickFormat(d3.timeFormat("%H:%M"));

    const yAxis = d3.axisLeft(yScale);

    // Add X axis
    svg.append("g")
      .attr("class", "x axis")
      .attr("transform", `translate(0,${height})`)
      .call(xAxis);

    // Add Y axis
    svg.append("g")
      .attr("class", "y axis")
      .call(yAxis);

    // Draw lines for each metric
    metricDatasets.forEach(dataset => {
      const line = d3.line()
        .x((d, i) => xScale(new Date(context.start() + i * context.step())))
        .y(d => yScale(d))
        .defined(d => d !== null && d !== undefined);

      // Collect values for this metric across the time range
      const values = [];
      for (let i = 0; i < size; i++) {
        values.push(dataset.graphiteMetric.valueAt(i));
      }

      // Draw the line
      svg.append("path")
        .datum(values)
        .attr("class", "line")
        .attr("fill", "none")
        .attr("stroke", dataset.color || "#000")
        .attr("stroke-width", 1.5)
        .attr("stroke-dasharray", dataset.dashed ? "5,5" : "0")
        .attr("d", line);

      // Add fill if requested
      if (dataset.fill) {
        const area = d3.area()
          .x((d, i) => xScale(new Date(context.start() + i * context.step())))
          .y0(height)
          .y1(d => yScale(d))
          .defined(d => d !== null && d !== undefined);

        svg.append("path")
          .datum(values)
          .attr("class", "area")
          .attr("fill", dataset.fillColor || `${dataset.color}33`) // Add transparency
          .attr("d", area);
      }
    });

    // Add legend
    const legend = svg.append("g")
      .attr("class", "legend")
      .attr("transform", `translate(${width + 5}, 0)`);

    metricDatasets.forEach((dataset, i) => {
      // Add color box
      legend.append("rect")
        .attr("x", 0)
        .attr("y", i * 25)
        .attr("width", 15)
        .attr("height", 15)
        .attr("fill", dataset.color || "#000");

      // Add metric name
      legend.append("text")
        .attr("x", 20)
        .attr("y", i * 25 + 12)
        .text(dataset.name || dataset.target);
    });

    // Add percentage calculation if requested
    if (showPercentage && metrics.length >= 2) {
      const metric1 = metricDatasets[0].graphiteMetric;
      const metric2 = metricDatasets[1].graphiteMetric;

      const percentageText = svg.append("text")
        .attr("class", "percentage")
        .attr("x", width - 120)
        .attr("y", 20)
        .attr("text-anchor", "end");

      // Update percentage on focus change
      context.on("focus", function(i) {
        if (i === null) return;

        const value1 = metric1.valueAt(i);
        const value2 = metric2.valueAt(i);

        if (value1 !== null && value2 !== null && value2 !== 0) {
          const percentage = (value1 / value2 * 100).toFixed(1);
          percentageText.text(`${metrics[0].name}/${metrics[1].name}: ${percentage}%`);
        } else {
          percentageText.text("");
        }
      });

      // Initial percentage calculation (latest data point)
      const latestIndex = size - 1;
      const value1 = metric1.valueAt(latestIndex);
      const value2 = metric2.valueAt(latestIndex);

      if (value1 !== null && value2 !== null && value2 !== 0) {
        const percentage = (value1 / value2 * 100).toFixed(1);
        percentageText.text(`${metrics[0].name}/${metrics[1].name}: ${percentage}%`);
      }
    }

    // Add focus tracking
    const focus = svg.append("g")
      .attr("class", "focus")
      .style("display", "none");

    focus.append("circle")
      .attr("r", 5);

    const focusText = focus.append("text")
      .attr("x", 9)
      .attr("dy", ".35em");

    const overlay = svg.append("rect")
      .attr("class", "overlay")
      .attr("width", width)
      .attr("height", height)
      .attr("fill", "none")
      .attr("pointer-events", "all");

    // Add mouse tracking events
    /* overlay.on("mouseover", () => focus.style("display", null))
      .on("mouseout", () => focus.style("display", "none"))
      .on("mousemove", function() {
        const mouse = d3.mouse(this);
        const x0 = xScale.invert(mouse[0]);
        const i = Math.floor((x0 - start) / context.step());

        if (i >= 0 && i < size) {
          // Update focus for each metric
          const values = metricDatasets.map(d => d.graphiteMetric.valueAt(i));

          if (values.some(v => v !== null)) {
            const tooltip = metricDatasets.map((d, idx) => {
              const val = values[idx];
              return val !== null ? `${d.name}: ${val.toFixed(2)}` : "";
            }).filter(Boolean).join("\n");

            focusText.text(tooltip);

            // Move focus to current point on first metric
            const primaryValue = values[0];
            if (primaryValue !== null) {
              focus.attr("transform", `translate(${mouse[0]},${yScale(primaryValue)})`);
            }

            // Update the rule line
            rule.style("display", null);
            rule.attr("transform", `translate(${mouse[0]},0)`);
            rule.select("text").text(d3.timeFormat("%H:%M:%S")(xScale.invert(mouse[0])));
          }
        }
      });
*/

    // Add styles directly to maintain consistent appearance
    d3.select(graphRef.current).selectAll(".line")
      .style("fill", "none")
      .style("stroke-width", "1.5px");

    d3.select(graphRef.current).selectAll(".axis path, .axis line")
      .style("fill", "none")
      .style("stroke", "#000")
      .style("shape-rendering", "crispEdges");

    d3.select(graphRef.current).selectAll(".overlay")
      .style("fill", "none")
      .style("pointer-events", "all");

    d3.select(graphRef.current).selectAll(".focus circle")
      .style("fill", "none")
      .style("stroke", "#000");

    d3.select(graphRef.current).selectAll(".title")
      .style("font-size", "14px")
      .style("font-weight", "bold");

    d3.select(graphRef.current).selectAll(".percentage")
      .style("font-size", "12px")
      .style("font-weight", "bold");

  }, [context, size, step, title, metrics, maxExtent, showPercentage]);

  return (
    <div className={className}>
      <div ref={graphRef} style={{ width: '100%', height: '100%' }}></div>
    </div>
  );
}

export default MultiMetricGraph;
