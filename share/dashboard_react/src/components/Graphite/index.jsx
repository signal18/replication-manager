import React, { useEffect, useRef } from 'react';
import '../../styles/_graphite.scss';
import { Box } from '@chakra-ui/react';
import * as d3 from 'd3';
import { useTheme } from '../../ThemeProvider';

function Graphite({
  chartRef,
  context,
  title,
  title2,
  target,
  target2,
  className,
  duration,
  height = 256,
  maxExtent = 1000
}) {
  const { theme } = useTheme();
  const mountedRef = useRef(false);

  // Theme variables
  const themeColors = {
    background: theme === 'light'
      ? 'var(--white-color, #ffffff)'
      : 'var(--body-bg-color, #131a34)',
    text: theme === 'light'
      ? 'var(--text-color, #333333)'
      : 'var(--text-color, #e7e9ef)',
    border: theme === 'light'
      ? 'var(--gray-color, #e2e8f0)'
      : 'var(--darkgray-color, #1a202c)',
    panelBackground: theme === 'light'
      ? 'var(--white-color, #ffffff)'
      : 'var(--secondary-gray-color, #2a3048)'
  };

  useEffect(() => {
    if (!chartRef.current || !context) return;

    // Clear any existing elements
    d3.select(chartRef.current).selectAll('*').remove();

    // Initialize graphite context
    let graphite = context.graphite('/graphite');
    let data = graphite.metric(target).alias(title || 'Metric 1');
    let data2 = null;

    if (title2 && target2) {
      data2 = graphite.metric(target2).alias(title2 || 'Metric 2');
    }

    const div = d3.select(chartRef.current)
      .style('position', 'relative')
      .style('background-color', themeColors.panelBackground)
      .style('border-radius', '8px')
      .style('overflow', 'hidden');

      // Render the chart
    const horizons = div
      .selectAll('.horizon')
      .data(data2 ? [data, data2] : [data])
      .enter()
      .append('div')
      .attr('class', 'horizon')
      .style('background-color', themeColors.panelBackground);

    // Create title elements manually to ensure correct values
    horizons.each(function(d, i) {
      context.axis().render(d3.select(this)
        .append('div')
        .attr('class', 'title')
        .text(i === 0 ? title || 'Metric 1' : title2 || 'Metric 2'));
    });

    context.horizon().extent([0, maxExtent]).height(height).render(horizons);

    // Create a tooltip container for displaying values at mouse position
    const tooltip = div.append('div')
      .attr('class', 'graphite-tooltip')
      .style('position', 'absolute')
      .style('top', '10px')  // Position at top of chart
      .style('background', themeColors.background === 'var(--white-color, #ffffff)' ? 'rgba(255, 255, 255, 0.9)' : 'rgba(42, 48, 72, 0.9)')
      .style('color', themeColors.text)
      .style('padding', '4px 8px')
      .style('border-radius', '4px')
      .style('font-size', '14px')
      .style('pointer-events', 'none') // Don't interfere with mouse events
      .style('z-index', '10')
      .style('display', 'none')
      .style('white-space', 'nowrap')
      .style('box-shadow', '0 1px 4px rgba(0,0,0,0.1)');

    // Create tooltip content elements
    const metrics = data2 ? [data, data2] : [data];
    const tooltipItems = tooltip.selectAll('.tooltip-item')
      .data(metrics)
      .enter()
      .append('div')
      .attr('class', 'tooltip-item')
      .style('margin', '2px 0')
      .text((d, i) => (i === 0 ? title : title2) || `Metric ${i+1}: -`);

    // Add axis
    context.axis().orient('top').render(div.append('div').attr('class', 'axis').style('background-color', themeColors.panelBackground));

    // Add rule for mouse tracking
    context.rule().render(div.append('div').attr('class', 'rule'));

    // Apply text color to all text elements
    div.selectAll('text').style('fill', themeColors.text);

    // On focus (mouseover), update the tooltip display
    context.on('focus', function(i) {
      if (i == null) {
        // Hide tooltip when not hovering
        tooltip.style('display', 'none');
      } else {
        // Show and position tooltip at mouse x-coordinate

        // Update tooltip content
        tooltipItems.each(function(d, idx) {
          if (!d) return;

          try {
            const value = typeof d.valueAt === 'function' ? d.valueAt(i) : null;
            const formattedValue = value !== null && value !== undefined
              ? d3.format(',.2f')(value)
              : 'N/A';
            const metricName = idx === 0 ? title || 'Metric 1' : title2 || 'Metric 2';

            d3.select(this).text(`${metricName}: ${formattedValue}`);
          } catch (error) {
            console.error('Error updating tooltip value:', error);
            d3.select(this).text(`${idx === 0 ? title : title2 || `Metric ${idx+1}`}: Error`);
          }
        });

        // Position tooltip at the mouse x-coordinate
        tooltip
          .style('display', 'block')
          .style('left', `${Math.max(0, i - 50)}px`);  // Center tooltip on cursor
      }
    });

    mountedRef.current = true;

    return () => {
      if (!mountedRef.current) return;
      // Remove the focus handler
      context.stop();
      // context.on('focus', null);
      // Clear the D3 selection
      div.selectAll('*').remove();
      // Clean up references
      data = null;
      data2 = null;
      graphite = null;
      mountedRef.current = false;
    };
  }, [context, chartRef, theme, title, title2, target, target2, maxExtent]);

  return (
    <Box
      className={className}
      ref={chartRef}
      sx={{
        backgroundColor: themeColors.panelBackground,
        borderRadius: '8px',
        overflow: 'hidden',
        '& .horizon': {
          backgroundColor: themeColors.panelBackground,
          borderColor: themeColors.border,
        },
        '& .axis': {
          backgroundColor: themeColors.panelBackground,
        },
        '& text': {
          fill: themeColors.text,
        }
      }}
    />
  );
}

export default Graphite;
