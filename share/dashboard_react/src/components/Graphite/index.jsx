import React, { useEffect, useRef } from 'react'
import '../../styles/_graphite.scss'
import { Box } from '@chakra-ui/react'

function Graphite({ chartRef, context, title, title2, target, target2, className, maxExtent = 1000 }) {
  useEffect(() => {
    if (!chartRef.current || !context) return
    d3.select(chartRef.current).selectAll('*').remove()
    let graphite = context.graphite('http://repman.marie-dev.svc.cloud18:10001/graphite')
    let data = graphite.metric(target).alias(title)
    let data2 = null
    if (title2 && target2) {
      data2 = graphite.metric(target2).alias(title2)
    }

    const div = d3.select(chartRef.current)

    // Render the chart
    div
      .selectAll('.horizon')
      .data(data2 ? [data, data2] : [data])
      .enter()
      .append('div')
      .attr('class', 'horizon')
      .call(context.horizon().extent([0, maxExtent]).height(256))

    div.append('div').attr('class', 'axis').call(context.axis().orient('top'))

    div.append('div').attr('class', 'rule').call(context.rule())

    // On mousemove, reposition the chart values to match the rule.
    context.on('focus', function (i) {
      d3.selectAll('.value').style(
        'right',
        i == null ? null : i < 30 ? context.size() - i - 40 + 'px' : context.size() - i + 'px'
      )
    })
    return () => {
      // Remove the focus handler
      console.log('context::', context)
      context.stop()
      context.on('focus', null)

      // Clear the D3 selection
      div.selectAll('*').remove()
      data = null
      data2 = null

      // Optionally, set the graphite variable to null (though not strictly necessary)
      graphite = null
    }
  }, [context, chartRef])
  return <Box className={className} ref={chartRef}></Box>
}

export default Graphite
