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
    let data = graphite.metric(target).alias(title);
    let data2 = null;

    if (title2 && target2) {
      data2 = graphite.metric(target2).alias(title2);
    }

    const div = d3.select(chartRef.current)
      .style('position', 'relative')
      .style('background-color', themeColors.panelBackground)
      .style('border-radius', '8px')
      .style('overflow', 'hidden');

    // Render the chart
    div
      .selectAll('.horizon')
      .data(data2 ? [data, data2] : [data])
      .enter()
      .append('div')
      .attr('class', 'horizon')
      .style('background-color', themeColors.panelBackground)
      .call(context.horizon().extent([0, maxExtent]).height(256));

    div.append('div')
      .attr('class', 'axis')
      .style('background-color', themeColors.panelBackground)
      .call(context.axis().orient('top'));

    div.append('div')
      .attr('class', 'rule')
      .call(context.rule());

    // Apply text color to all text elements
    div.selectAll('text')
      .style('fill', themeColors.text);

    // Apply background color to specific elements
    div.selectAll('.title, .value')
      .style('color', themeColors.text);

    // On mousemove, reposition the chart values to match the rule
    context.on('focus', function(i) {
      d3.selectAll('.value').style(
        'right',
        i == null ? null : i < 30 ? context.size() - i - 40 + 'px' : context.size() - i + 'px'
      );
    });

    mountedRef.current = true;

    return () => {
      if (!mountedRef.current) return;

      // Remove the focus handler
      context.stop();
      context.on('focus', null);

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
