import React, { useRef } from 'react';
import { Box } from '@chakra-ui/react';
import '../../styles/_graphite.scss';

function MutexTracingGraph({ context, size, step, className, maxExtent = 1000, clusterConfig }) {
  const chartRef = useRef(null);

  // Array of mutex metrics to display
  const mutexMetrics = [
    'mysql_global_status_wait_synch_mutex_innodb_dict_foreign_err_mutex',
    'mysql_global_status_wait_synch_mutex_innodb_fil_system_mutex',
    'mysql_global_status_wait_synch_mutex_innodb_flush_list_mutex',
    'mysql_global_status_wait_synch_mutex_innodb_lock_wait_mutex',
    'mysql_global_status_wait_synch_mutex_innodb_trx_sys_mutex'
  ];

  // Convert long mutex names to shorter display names
  const getShortMutexName = (metricName) => {
    const parts = metricName.split('_');
    return parts[7] + '_' + parts[8]+ '_' + parts[9];
  };

  React.useEffect(() => {
    if (!chartRef.current || !context) return;

    d3.select(chartRef.current).selectAll('*').remove();

    // Connect to graphite
    let graphite = context.graphite('/graphite');

    // Create a series of metrics for each mutex type
    const metrics = mutexMetrics.map(metric => {
      // Sum the metrics across all servers like your example does
      const path = `perSecond(sumSeries(mysql.*.${metric}))`;
      return graphite.metric(path).alias(getShortMutexName(metric));
    });

    const div = d3.select(chartRef.current).style('position', 'relative');

    // Render each metric as a horizon chart
    div
      .selectAll('.horizon')
      .data(metrics)
      .enter()
      .append('div')
      .attr('class', 'horizon')
      .call(context.horizon().extent([0, maxExtent]).height(40));

    div.append('div').attr('class', 'axis').call(context.axis().orient('top'));
    div.append('div').attr('class', 'rule').call(context.rule());

    // On mousemove, reposition the chart values to match the rule
    context.on('focus', function(i) {
      d3.selectAll('.value').style(
        'right',
        i == null ? null : i < 30 ? context.size() - i - 40 + 'px' : context.size() - i + 'px'
      );
    });

    return () => {
      // Remove the focus handler
      context.stop();
      context.on('focus', null);
      // Clear the D3 selection
      div.selectAll('*').remove();
    };
  }, [context, maxExtent, clusterConfig]);

  // Only render the component if monitoringPerformanceSchemaLatch is true
  if (!clusterConfig || !clusterConfig?.monitoringPerformanceSchemaLatch) {
    return null;
  }
  return (
  <div className="graph-container">
      <h3>Mutex Tracing</h3>
      <Box className={className} ref={chartRef}></Box>
    </div>
  );
}

export default MutexTracingGraph;
